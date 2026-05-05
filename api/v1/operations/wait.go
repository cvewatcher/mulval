package operations

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cvewatcher/mulval/api/v1/analysis"
	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
)

const (
	// maxWaitTimeout caps the server-side wait duration per AIP-151.
	maxWaitTimeout = 10 * time.Minute

	// defaultWaitTimeout is used when the client sends no timeout.
	defaultWaitTimeout = maxWaitTimeout
)

// WaitOperation blocks until the named operation reaches a terminal state or
// the timeout elapses, whichever comes first.
//
// AIP-151 contract:
//   - If the operation is already terminal, return it immediately.
//   - If the timeout elapses before completion, return the current
//     (not-done) operation without error.
//   - The maximum server-side timeout is maxWaitTimeout; longer client
//     timeouts are silently capped.
//
// Implementation uses a JetStream ordered consumer on the per-operation
// subject so the handler wakes immediately when MulVAL finishes, without
// polling the database.
//
// Race safety: the consumer is created *before* the second state check.
// This closes the window where completion fires between the first check
// and the subscribe call.
func (s *OperationServer) WaitOperation(
	ctx context.Context,
	req *longrunningpb.WaitOperationRequest,
) (*longrunningpb.Operation, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// Fast path: already terminal — no subscription needed.
	op, err := store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get operation: %v", err)
	}
	if op == nil {
		return nil, status.Errorf(codes.NotFound, "operation %q not found", req.Name)
	}
	if op.State.IsTerminal() {
		return analysis.OperationFromStore(op), nil
	}

	// Determine effective timeout.
	timeout := defaultWaitTimeout
	if req.Timeout != nil {
		if d := req.Timeout.AsDuration(); d > 0 && d < maxWaitTimeout {
			timeout = d
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Subscribe *before* the second state check to close the race window
	// where completion fires between the first check and subscribe.
	ch, stop, err := global.GetNatsManager().SubscribeOperationUpdate(waitCtx, req.Name)
	if err != nil {
		// NATS unavailable: fall back to a single long poll at timeout.
		// The client will need to retry with GetOperation if needed.
		<-waitCtx.Done()
		op, _ = store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
		if op == nil {
			return nil, status.Errorf(codes.NotFound,
				"operation %q not found", req.Name)
		}
		return analysis.OperationFromStore(op), nil
	}
	defer stop()

	// Second state check after subscribing.
	op, err = store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get operation: %v", err)
	}
	if op == nil {
		return nil, status.Errorf(codes.NotFound,
			"operation %q not found", req.Name)
	}
	if op.State.IsTerminal() {
		return analysis.OperationFromStore(op), nil
	}

	// Wait for the completion notification or timeout.
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				// Channel closed (consumer stopped or context cancelled).
				op, _ = store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
				if op == nil {
					return nil, status.Errorf(codes.NotFound,
						"operation %q not found", req.Name)
				}
				return analysis.OperationFromStore(op), nil
			}
			// Acknowledge and re-read authoritative state from DB.
			_ = msg.Ack()
			op, err = store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
			if err != nil {
				return nil, status.Errorf(codes.Internal,
					"get operation after notify: %v", err)
			}
			if op == nil {
				return nil, status.Errorf(codes.NotFound,
					"operation %q not found", req.Name)
			}
			if op.State.IsTerminal() {
				return analysis.OperationFromStore(op), nil
			}
			// Not yet terminal — keep waiting (should not happen in practice
			// since PublishOperationUpdate is called after DB write, but
			// guard against spurious messages).

		case <-waitCtx.Done():
			// AIP-151: timeout is not an error — return current state.
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				op, _ = store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
				if op == nil {
					return nil, status.Errorf(codes.NotFound,
						"operation %q not found", req.Name)
				}
				return analysis.OperationFromStore(op), nil
			}
			// Parent context cancelled — propagate.
			return nil, status.Errorf(codes.Canceled,
				"wait cancelled: %v", waitCtx.Err())
		}
	}
}
