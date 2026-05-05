package executor

import (
	"context"
	"sync"

	"github.com/cvewatcher/mulval/pkg/monitoring"
	"github.com/cvewatcher/mulval/pkg/services/nats"
	"github.com/cvewatcher/mulval/pkg/services/pgsql"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Executor is the core execution engine. It pulls work messages from NATS, runs the Pulumi
// executor, and publishes events back.
type Executor struct {
	rootCtx context.Context
	config  ExecutorConfig

	// wg tracks all handleMessage goroutines so Wait() can block until they all return cleanly.
	wg sync.WaitGroup
}

type ExecutorConfig struct {
	Logger       *zap.Logger
	Metrics      *monitoring.Metrics
	Tracer       trace.Tracer
	PgsqlManager *pgsql.Manager
	NatsManager  *nats.Manager
}

func New(rootCtx context.Context, config ExecutorConfig) *Executor {
	return &Executor{
		rootCtx: rootCtx,
		config:  config,
	}
}

// Wait blocks until all in-flight operations have reached a terminal state and their goroutines
// have returned. Intended to be called after the root context is cancelled to ensure a clean
// shutdown before the process exits.
func (e *Executor) Wait(ctx context.Context) {
	e.config.Logger.Info("waiting for in-flight operations to complete...")
	e.wg.Wait()
	e.config.Logger.Info("all operations completed")
}
