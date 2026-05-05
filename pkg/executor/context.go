package executor

import (
	"context"
	"sync"

	apiv1 "github.com/cvewatcher/mulval/api/v1"
	"github.com/cvewatcher/mulval/pkg/monitoring"
	"github.com/cvewatcher/mulval/pkg/services/nats"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// RunContext composes all the contexts a runner goroutine needs.
//
// Sources of cancellation:
//   - ctx: the RPC request context, used only for initial setup.
//     Once the work has begun, can only be stopped by other sources.
//   - root: the application lifetime context (SIGTERM/SIGINT).
//     When cancelled, the operation can be retried by another executor.
//   - an explicit CancelOperation RPC. Abort fires with reason=API.
type RunContext struct {
	// Abort is cancelled when the operation should stop.
	Abort context.Context

	// Store is used for all DB writes. Never cancelled.
	Store context.Context

	cancel   context.CancelFunc
	reasonMu sync.Mutex
	reason   string

	config RunContextConfig
}

type RunContextConfig struct {
	RootCtx, ReqCtx context.Context
	OpName          string
	NatsManager     *nats.Manager
	Logger          *zap.Logger
	Metrics         *monitoring.Metrics
	Tracer          trace.Tracer
}

func NewRunContext(config RunContextConfig) *RunContext {
	// Carry the active span from the RPC request into the goroutine's context
	// without inheriting the request's cancellation.
	// trace.SpanFromContext returns a no-op span if reqCtx carries none,
	// so this is safe even without OTel configured.
	//
	// Do the same for deployment and operation names.
	storeCtx := trace.ContextWithSpan(config.RootCtx, trace.SpanFromContext(config.ReqCtx))
	// storeCtx = global.WithOperationName(storeCtx, config.OpName)

	abort, cancel := context.WithCancel(storeCtx)
	rc := &RunContext{
		Abort:  abort,
		Store:  context.WithoutCancel(storeCtx),
		cancel: cancel,
		config: config,
	}

	go rc.listenForCancellation(storeCtx, config.OpName)

	return rc
}

// Finish cancels Abort from the operation side - call this with defer at
// the top of every runner goroutine so the LISTEN goroutine exits cleanly
// when the operation completes normally.
func (rc *RunContext) Finish() {
	rc.cancel()
}

// Reason returns the cancellation reason.
// Only meaningful after Abort.Done() has fired.
func (rc *RunContext) Reason() string {
	rc.reasonMu.Lock()
	defer rc.reasonMu.Unlock()
	return rc.reason
}

// listenForCancellation acquires a dedicated LISTEN connection using reqCtx,
// then blocks on rc.Abort waiting for a cancel_operation_{opID} notification.
// Exits cleanly when rc.Abort is cancelled for any reason.
func (rc *RunContext) listenForCancellation(ctx context.Context, opName string) {
	if err := rc.config.NatsManager.SubscribeCancel(ctx, opName, func(req apiv1.Cancel) {
		rc.reasonMu.Lock()
		rc.reason = req.Reason
		rc.reasonMu.Unlock()
		rc.cancel()

		rc.config.Logger.Info("cancel signal received",
			zap.String("operation", opName),
			zap.String("reason", req.Reason),
		)
		if opName != req.Operation {
			rc.config.Logger.Warn("cancel operation ID mistmatch",
				zap.String("operation", opName),
				zap.String("cancel_operation", req.Operation),
			)
		}
	}); err != nil {
		return
	}
}
