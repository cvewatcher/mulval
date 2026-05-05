package operations

import (
	"context"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/cvewatcher/mulval/api/v1/analysis"
	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetOperation returns the current state of an operation.
//
// AIP-151: the operation name must be in the form "operations/{uuid}".
// The response is done=true once the operation reaches any terminal state.
func (s *OperationServer) GetOperation(
	ctx context.Context,
	req *longrunningpb.GetOperationRequest,
) (*longrunningpb.Operation, error) {
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

	return analysis.OperationFromStore(op), nil
}
