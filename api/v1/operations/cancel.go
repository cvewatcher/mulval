package operations

import (
	"context"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
)

// CancelOperation requests cancellation of a running operation.
//
// AIP-151 contract:
//   - Cancellation is best-effort: the operation may already be complete
//     by the time this RPC is processed.
//   - If the operation is already in a terminal state, the call succeeds
//     without modifying anything.
//   - On success the DB row is transitioned to CANCELLED and a cancel
//     signal is published to NATS so the executor goroutine stops the
//     MulVAL subprocess.
//
// The two-step sequence (DB write → NATS publish) is intentional:
// the DB is the authoritative state. If NATS delivery fails the row
// is already marked CANCELLED and the executor will detect the state
// on its next check or at process restart.
func (s *OperationServer) CancelOperation(
	ctx context.Context,
	req *longrunningpb.CancelOperationRequest,
) (*emptypb.Empty, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	op, err := store.GetByName(ctx, global.GetPgSQLManager(), req.Name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get operation: %v", err)
	}
	if op == nil {
		return nil, status.Errorf(codes.NotFound, "operation %q not found", req.Name)
	}

	// Already terminal — nothing to do. AIP-151 says this should succeed.
	if op.State.IsTerminal() {
		return &emptypb.Empty{}, nil
	}

	// Write CANCELLED to the DB first — authoritative state.
	if _, err := store.MarkCancelled(ctx, global.GetPgSQLManager(), op); err != nil {
		return nil, status.Errorf(codes.Internal, "mark cancelled: %v", err)
	}

	// Publish the cancel signal to NATS so the executor kills the subprocess.
	// Best-effort: if NATS is unavailable the executor will eventually
	// observe the CANCELLED state from the DB on its next poll or restart.
	_ = global.GetNatsManager().PublishCancel(ctx, "operations-server", req.Name, "CancelOperation RPC")

	return &emptypb.Empty{}, nil
}
