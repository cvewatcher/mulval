package server

import (
	"context"
	"net/http"
	"time"

	"github.com/cvewatcher/mulval/global"
	"github.com/hellofresh/health-go/v5"
	"go.uber.org/multierr"
)

func healthcheck(_ context.Context) http.Handler {
	opts := []health.Option{
		health.WithComponent(health.Component{
			Name:    "deployer",
			Version: global.Version,
		}),
		health.WithSystemInfo(),
	}
	h, err := health.New(opts...)
	if err != nil {
		panic(err)
	}

	// PostgreSQL
	_ = h.Register(health.Config{
		Name:    "postgresql",
		Timeout: 3 * time.Second,
		Check: func(ctx context.Context) error {
			return multierr.Combine(
				global.GetPgSQLManager().Healthcheck(ctx),
				global.GetPgSQLManager().Healthcheck(ctx),
			)
		},
	})

	return h.Handler()
}
