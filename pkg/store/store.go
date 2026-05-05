// Package store provides the persistence layer for MulVAL analyses.
//
// It owns all SQL and acts as the single point of contact between the
// application code (API handlers, executor) and the PostgreSQL manager.
// Neither the API layer nor the executor import pkg/services/pgsql directly.
//
// Page token format: base64url(create_time_rfc3339 + "," + operation_name).
// This gives stable cursor pagination even when new rows are inserted between
// pages, which is required for drift detection by downstream consumers.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/cvewatcher/mulval/pkg/services/pgsql"
	"github.com/google/uuid"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// State enumerates the lifecycle states of an Analysis.
type State string

const (
	StateRunning   State = "RUNNING"
	StateSucceeded State = "SUCCEEDED"
	StateFailed    State = "FAILED"
	StateCancelled State = "CANCELLED"
)

// IsTerminal reports whether s is a terminal state.
func (s State) IsTerminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled:
		return true
	}
	return false
}

// Operation is the in-memory representation of one MulVAL analysis.
// Passed between API handlers and the executor to avoid context stuffing.
type Operation struct {
	// OperationName is the AIP-151 LRO name: "operations/{uuid}".
	// It doubles as the analysis resource name: "analyses/{uuid}".
	OperationName string

	// InputHash is the SHA-256 fingerprint of EDB+IDB.
	InputHash string

	// EDBFacts and IDBRules are the raw Prolog inputs, newline-joined.
	EDBFacts string
	IDBRules string

	// State is the current lifecycle state.
	State State

	// CreateTime is when the row was first inserted.
	CreateTime time.Time

	// EndTime is set when the operation reaches a terminal state.
	EndTime *time.Time

	// Error holds the failure message when State == StateFailed.
	Error *string

	// Output is nil until State == StateSucceeded.
	Output *OperationOutput
}

// OperationOutput holds the raw MulVAL output files.
type OperationOutput struct {
	VerticesCSV string
	ArcsCSV     string
	// Summary is the content of AttackGraph.txt. May be empty.
	Summary string
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// HashInputs produces a deterministic SHA-256 fingerprint of edb+idb.
func HashInputs(edb, idb string) string {
	h := sha256.New()
	h.Write([]byte(edb))
	h.Write([]byte{0})
	h.Write([]byte(idb))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// NewOperationName generates a new AIP-151 operation name.
func NewOperationName() string {
	return "operations/" + uuid.NewString()
}

// AnalysisNameFromOperation converts "operations/{uuid}" → "analyses/{uuid}".
func AnalysisNameFromOperation(opName string) string {
	return strings.Replace(opName, "operations/", "analyses/", 1)
}

// OperationNameFromAnalysis converts "analyses/{uuid}" → "operations/{uuid}".
func OperationNameFromAnalysis(analysisName string) string {
	return strings.Replace(analysisName, "analyses/", "operations/", 1)
}

// UUIDFromName extracts the UUID from either "operations/{uuid}" or
// "analyses/{uuid}".
func UUIDFromName(name string) string {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return name
}

// ── Page token ────────────────────────────────────────────────────────────────

// pageCursor is the decoded form of a ListAnalyses page token.
type pageCursor struct {
	CreateTime    time.Time
	OperationName string
}

// encodeCursor encodes a page cursor as a base64url string safe for use as
// a page token in the API response.
func encodeCursor(c pageCursor) string {
	raw := c.CreateTime.UTC().Format(time.RFC3339Nano) + "," + c.OperationName
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor decodes a page token back to a pageCursor.
// Returns an error if the token is malformed.
func decodeCursor(token string) (pageCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return pageCursor{}, fmt.Errorf("store: invalid page token: %w", err)
	}
	parts := strings.SplitN(string(b), ",", 2)
	if len(parts) != 2 {
		return pageCursor{}, fmt.Errorf("store: malformed page token")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return pageCursor{}, fmt.Errorf("store: invalid page token timestamp: %w", err)
	}
	return pageCursor{CreateTime: t, OperationName: parts[1]}, nil
}

// ── Write operations ──────────────────────────────────────────────────────────

// CreateAnalysis inserts a new analysis row with state=RUNNING.
//
// If the operation name already exists (idempotent retry) the existing row is
// returned unchanged — callers can inspect op.State to determine whether a
// new run was started or an existing one was found.
//
// Returns (op, true, nil) when a new row was created.
// Returns (op, false, nil) when the row already existed.
func CreateAnalysis(
	ctx context.Context,
	mgr *pgsql.Manager,
	opName, edb, idb string,
) (*Operation, bool, error) {
	hash := HashInputs(edb, idb)

	// Attempt insert.
	tag, err := mgr.Exec(ctx, `
		INSERT INTO analyses (operation_name, input_hash, edb_facts, idb_rules, state)
		VALUES ($1, $2, $3, $4, 'RUNNING')
		ON CONFLICT (operation_name) DO NOTHING`,
		opName, hash, edb, idb,
	)
	if err != nil {
		return nil, false, fmt.Errorf("store: create analysis %q: %w", opName, err)
	}

	created := tag.RowsAffected() == 1

	// Fetch the row regardless of whether we just created it or it existed.
	op, err := GetByName(ctx, mgr, opName)
	if err != nil {
		return nil, false, err
	}
	if op == nil {
		return nil, false, fmt.Errorf("store: analysis %q disappeared after insert", opName)
	}
	return op, created, nil
}

// MarkSucceeded transitions op to StateSucceeded and stores the raw outputs.
// Pass rc.Store (never-cancelled context) to survive request teardown.
func MarkSucceeded(
	ctx context.Context,
	mgr *pgsql.Manager,
	op *Operation,
	output *OperationOutput,
) (*Operation, error) {
	row, err := mgr.QueryRow(ctx, `
		UPDATE analyses
		SET state        = 'SUCCEEDED',
		    end_time     = now(),
		    vertices_csv = $2,
		    arcs_csv     = $3,
		    summary      = NULLIF($4, '')
		WHERE operation_name = $1
		RETURNING `+allColumns,
		op.OperationName,
		output.VerticesCSV,
		output.ArcsCSV,
		output.Summary,
	)
	if err != nil {
		return nil, fmt.Errorf("store: mark succeeded %q: %w", op.OperationName, err)
	}
	return scan(row)
}

// MarkFailed transitions op to StateFailed with the given error message.
func MarkFailed(
	ctx context.Context,
	mgr *pgsql.Manager,
	op *Operation,
	errMsg string,
) (*Operation, error) {
	row, err := mgr.QueryRow(ctx, `
		UPDATE analyses
		SET state    = 'FAILED',
		    end_time = now(),
		    error    = $2
		WHERE operation_name = $1
		RETURNING `+allColumns,
		op.OperationName, errMsg,
	)
	if err != nil {
		return nil, fmt.Errorf("store: mark failed %q: %w", op.OperationName, err)
	}
	return scan(row)
}

// MarkCancelled transitions op to StateCancelled.
// Called by the CancelOperation RPC handler — not the executor.
func MarkCancelled(
	ctx context.Context,
	mgr *pgsql.Manager,
	op *Operation,
) (*Operation, error) {
	row, err := mgr.QueryRow(ctx, `
		UPDATE analyses
		SET state    = 'CANCELLED',
		    end_time = now()
		WHERE operation_name = $1
		RETURNING `+allColumns,
		op.OperationName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: mark cancelled %q: %w", op.OperationName, err)
	}
	return scan(row)
}

// ── Read operations ───────────────────────────────────────────────────────────

// GetByName retrieves an analysis by its operation name.
// Returns nil, nil when not found.
func GetByName(
	ctx context.Context,
	mgr *pgsql.Manager,
	opName string,
) (*Operation, error) {
	row, err := mgr.QueryRow(ctx, `
		SELECT `+allColumns+`
		FROM analyses
		WHERE operation_name = $1`,
		opName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get analysis %q: %w", opName, err)
	}
	op, err := scan(row)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: scan analysis %q: %w", opName, err)
	}
	return op, nil
}

// GetByHash looks up a SUCCEEDED analysis by its input content hash.
// Returns nil, nil when none found. Used for cache lookup.
func GetByHash(
	ctx context.Context,
	mgr *pgsql.Manager,
	hash string,
) (*Operation, error) {
	row, err := mgr.QueryRow(ctx, `
		SELECT `+allColumns+`
		FROM analyses
		WHERE input_hash = $1 AND state = 'SUCCEEDED'
		LIMIT 1`,
		hash,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get analysis by hash: %w", err)
	}
	op, err := scan(row)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: scan analysis by hash: %w", err)
	}
	return op, nil
}

// ListResult is the return value of ListAnalyses.
type ListResult struct {
	Operations    []*Operation
	NextPageToken string // empty when no further pages exist
}

// ListAnalyses returns a page of analyses ordered by (create_time DESC,
// operation_name). Pagination uses an opaque cursor token.
//
// pageSize must be between 1 and 1000; it is clamped by the caller.
// pageToken is empty for the first page.
func ListAnalyses(
	ctx context.Context,
	mgr *pgsql.Manager,
	pageSize int,
	pageToken string,
) (*ListResult, error) {
	// We fetch pageSize+1 rows to determine whether a next page exists,
	// then return only pageSize rows to the caller.
	limit := pageSize + 1

	var (
		rows interface {
			Close()
			Next() bool
			Scan(...any) error
			Err() error
		}
		err error
	)

	if pageToken == "" {
		// First page: no cursor, just order and limit.
		rows, err = mgr.Query(ctx, `
			SELECT `+allColumns+`
			FROM analyses
			ORDER BY create_time DESC, operation_name
			LIMIT $1`,
			limit,
		)
	} else {
		cursor, cerr := decodeCursor(pageToken)
		if cerr != nil {
			return nil, cerr
		}
		// Keyset pagination: return rows strictly "after" the cursor position
		// in (create_time DESC, operation_name) order.
		rows, err = mgr.Query(ctx, `
			SELECT `+allColumns+`
			FROM analyses
			WHERE (create_time, operation_name) < ($1, $2)
			ORDER BY create_time DESC, operation_name
			LIMIT $3`,
			cursor.CreateTime, cursor.OperationName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list analyses: %w", err)
	}
	defer rows.Close()

	ops := make([]*Operation, 0, pageSize)
	for rows.Next() {
		op, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan list row: %w", err)
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list rows error: %w", err)
	}

	var nextToken string
	if len(ops) == limit {
		// There is a next page. Return only pageSize rows and encode the
		// last returned row as the cursor.
		ops = ops[:pageSize]
		last := ops[pageSize-1]
		nextToken = encodeCursor(pageCursor{
			CreateTime:    last.CreateTime,
			OperationName: last.OperationName,
		})
	}

	return &ListResult{
		Operations:    ops,
		NextPageToken: nextToken,
	}, nil
}

// ── Scanning ──────────────────────────────────────────────────────────────────

// allColumns is the canonical SELECT column list, matching the scan order.
const allColumns = `operation_name, input_hash, edb_facts, idb_rules, state,
	       create_time, end_time, error,
	       vertices_csv, arcs_csv, summary`

type scannable interface {
	Scan(dest ...any) error
}

func scan(row scannable) (*Operation, error) {
	var (
		op          Operation
		stateStr    string
		endTime     *time.Time
		errMsg      *string
		verticesCSV *string
		arcsCSV     *string
		summary     *string
	)
	if err := row.Scan(
		&op.OperationName,
		&op.InputHash,
		&op.EDBFacts,
		&op.IDBRules,
		&stateStr,
		&op.CreateTime,
		&endTime,
		&errMsg,
		&verticesCSV,
		&arcsCSV,
		&summary,
	); err != nil {
		return nil, err
	}

	op.State = State(stateStr)
	op.EndTime = endTime
	op.Error = errMsg

	if verticesCSV != nil && arcsCSV != nil {
		sum := ""
		if summary != nil {
			sum = *summary
		}
		op.Output = &OperationOutput{
			VerticesCSV: *verticesCSV,
			ArcsCSV:     *arcsCSV,
			Summary:     sum,
		}
	}
	return &op, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows")
}
