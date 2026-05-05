package pgsql

import (
	"context"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var Migrations embed.FS

// Manager manages a pgxpool connection with automatic reconnection and health-check debouncing.
type Manager struct {
	mu     sync.RWMutex
	pool   *pgxpool.Pool
	config Config

	hcMu      sync.Mutex
	lastHc    time.Time
	lastHcErr error
}

// Config holds all parameters needed to connect and operate the Manager.
type Config struct {
	// DSN is the libpq-style connection string, e.g.:
	//   postgres://user:pass@localhost:5432/dbname
	// Do not append search_path manually - set Schema instead.
	DSN string

	// Schema is the PostgreSQL schema (namespace) to target.
	// When non-empty the manager:
	//   - appends ?search_path=<schema> to the DSN so pgxpool targets it
	//   - issues CREATE SCHEMA IF NOT EXISTS in Migrate()
	//   - sets search_path on the migration connection explicitly
	Schema string

	// The minimum connections to the PostgreSQL pool.
	MinConns int32

	// The maximum connections to the PostgreSQL pool.
	MaxConns int32

	Logger *zap.Logger
	Tracer trace.Tracer
	Meter  metric.Meter
}

// NewManager creates a new PostgreSQL manager.
func NewManager(config Config) *Manager {
	if config.Schema != "" {
		config.DSN = fmt.Sprintf("%s?search_path=%s", config.DSN, config.Schema)
	}
	return &Manager{config: config}
}

// Pool returns the underlying *pgxpool.Pool, reconnecting if necessary.
func (m *Manager) Pool(ctx context.Context) (*pgxpool.Pool, error) {
	return m.getPool(context.WithoutCancel(ctx))
}

// Exec executes a statement that returns no rows (INSERT, UPDATE, DELETE, ...).
func (m *Manager) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	pool, err := m.getPool(context.WithoutCancel(ctx))
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return pool.Exec(ctx, query, args...)
}

// QueryRow executes a query expected to return at most one row.
// Scan errors are deferred to the returned pgx.Row.
func (m *Manager) QueryRow(ctx context.Context, query string, args ...any) (pgx.Row, error) {
	pool, err := m.getPool(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	return pool.QueryRow(ctx, query, args...), nil
}

// Query executes a query that returns rows. The caller must close the returned
// pgx.Rows when done.
func (m *Manager) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	pool, err := m.getPool(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	return pool.Query(ctx, query, args...)
}

// WithTx runs fn inside a transaction, committing on success and rolling back
// on any error or panic.
func (m *Manager) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) (err error) {
	pool, err := m.getPool(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Migrate runs all pending up-migrations from the embedded Migrations FS.
// Call this once at service startup, before the service accepts traffic.
//
// Migration files must follow the golang-migrate naming convention:
//
//	migrations/000001_description.up.sql
//	migrations/000001_description.down.sql
func (m *Manager) Migrate(ctx context.Context) error {
	// 1. Ensure the target schema exists (uses the runtime pgxpool).
	if m.config.Schema != "" {
		pool, err := m.getPool(ctx)
		if err != nil {
			return fmt.Errorf("migrate: get pool: %w", err)
		}
		if _, err := pool.Exec(ctx,
			fmt.Sprintf(
				"CREATE SCHEMA IF NOT EXISTS %s",
				pgx.Identifier{m.config.Schema}.Sanitize(),
			),
		); err != nil {
			return fmt.Errorf("migrate: create schema %q: %w", m.config.Schema, err)
		}
	}

	// 2. Open a short-lived *sql.DB via pgx's stdlib adapter.
	//    ParseConfig uses the base DSN (search_path already in query string),
	//    but we will also SET it explicitly below to be certain.
	connCfg, err := pgx.ParseConfig(m.config.DSN)
	if err != nil {
		return fmt.Errorf("migrate: parse DSN: %w", err)
	}
	sqlDB := stdlib.OpenDB(*connCfg)
	defer func() {
		_ = sqlDB.Close()
	}()

	// 3. Explicitly set search_path on the single underlying connection.
	//    stdlib.OpenDB with a ConnConfig (not a DSN string) opens exactly
	//    one connection, so this SET is guaranteed to be in effect for all
	//    statements that golang-migrate executes.
	if m.config.Schema != "" {
		if _, err := sqlDB.ExecContext(ctx,
			fmt.Sprintf(
				"SET search_path TO %s",
				pgx.Identifier{m.config.Schema}.Sanitize(),
			),
		); err != nil {
			return fmt.Errorf("migrate: set search_path: %w", err)
		}
	}

	// 4. Wire up golang-migrate.
	src, err := iofs.New(Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: open embedded source: %w", err)
	}

	drv, err := migratepg.WithInstance(sqlDB, &migratepg.Config{
		SchemaName:      m.config.Schema,
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("migrate: create driver: %w", err)
	}

	mg, err := migrate.NewWithInstance("iofs", src, "postgres", drv)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}

	// Check for dirty state before attempting to run migrations.
	// A dirty state means a previous migration run failed mid-way and
	// requires manual intervention before the service can start.
	version, dirty, err := mg.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("migrate: get version: %w", err)
	}
	if dirty {
		return fmt.Errorf(
			"migrate: schema is dirty at version %d - manual intervention required: "+
				"repair the schema then run 'migrate force %d' (to revert) or "+
				"'migrate force %d' (if you completed it manually) before restarting",
			version, version-1, version,
		)
	}

	if err := mg.Up(); err != nil && err != migrate.ErrNoChange {
		// Check if the failure left a dirty state.
		version, dirty, verr := mg.Version()
		if verr == nil && dirty {
			return fmt.Errorf(
				"migrate: migration %d failed and left schema in a dirty state - "+
					"repair the schema then run 'migrate force %d' (to revert) or "+
					"'migrate force %d' (if you completed it manually) before restarting: %w",
				version, version-1, version, err,
			)
		}
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// Healthcheck verifies that the pool can reach PostgreSQL.
func (m *Manager) Healthcheck(ctx context.Context) error {
	_, err := m.getPool(context.WithoutCancel(ctx))
	return err
}

// Close shuts down the connection pool.
func (m *Manager) Close(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pool != nil {
		m.pool.Close()
		m.pool = nil
	}
}

func (m *Manager) getPool(ctx context.Context) (*pgxpool.Pool, error) {
	m.mu.RLock()
	pool := m.pool
	m.mu.RUnlock()

	if pool != nil {
		if err := m.healthcheck(ctx, pool); err == nil {
			return pool, nil
		}
	}

	return m.recreatePool(ctx)
}

func (m *Manager) recreatePool(ctx context.Context) (*pgxpool.Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-checked locking: another goroutine may have reconnected already.
	if m.pool != nil {
		if err := m.healthcheck(ctx, m.pool); err == nil {
			return m.pool, nil
		}
		m.pool.Close()
		m.pool = nil

		m.hcMu.Lock()
		m.lastHc = time.Time{}
		m.lastHcErr = nil
		m.hcMu.Unlock()
	}

	cfg, err := pgxpool.ParseConfig(m.config.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}
	cfg.MinConns = m.config.MinConns
	cfg.MaxConns = max(m.config.MinConns, m.config.MaxConns)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	// Eager liveness check
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	m.pool = pool
	return pool, nil
}

// healthcheckWindow is the minimum interval between liveness probes.
// 10 seconds avoids storming PostgreSQL with pings under high load.
const healthcheckWindow = 10 * time.Second

func (m *Manager) healthcheck(ctx context.Context, pool *pgxpool.Pool) error {
	now := time.Now()
	m.hcMu.Lock()
	defer m.hcMu.Unlock()

	// Fast path: a recent successful check is still valid.
	if now.Sub(m.lastHc) < healthcheckWindow && m.lastHcErr == nil {
		return nil
	}

	// Slow path: actually probe PostgreSQL.
	err := pool.Ping(ctx)

	m.lastHc = time.Now()
	m.lastHcErr = err

	return err
}
