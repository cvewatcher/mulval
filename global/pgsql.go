package global

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/cvewatcher/mulval/pkg/services/pgsql"
)

var (
	pgsqlInstance *pgsql.Manager
)

func InitPostgreSQL() (err error) {
	pgsqlInstance = pgsql.NewManager(pgsql.Config{
		DSN:      Config.Storage.DSN,
		Logger:   Log().Sub,
		Tracer:   Tracer,
		Meter:    Meter,
		Schema:   Config.Storage.Schema,
		MinConns: Config.Storage.MinConns,
		MaxConns: Config.Storage.MaxConns,
	})

	if _, err := Meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			val := int64(1)
			if err := pgsqlInstance.Healthcheck(ctx); err != nil {
				val = 0
			}
			o.ObserveInt64(GetMetrics().PostgreSQLUp, val)
			return nil
		},
		GetMetrics().PostgreSQLUp,
	); err != nil {
		return err
	}

	return nil
}

func GetPgSQLManager() *pgsql.Manager {
	return pgsqlInstance
}
