package analysis

import (
	"context"

	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
	analysispb "github.com/cvewatcher/mulval/proto/api/v1/analysis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetAnalysis implements AIP-131.
//
// The resource name may be provided in either its analysis form
// ("analyses/{uuid}") or its operation form ("operations/{uuid}") —
// both map to the same row. The returned Analysis carries the analysis
// resource name.
func (a *Analyzer) GetAnalysis(
	ctx context.Context,
	req *analysispb.GetAnalysisRequest,
) (*analysispb.Analysis, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// Normalise to operation name for the DB lookup.
	opName := store.OperationNameFromAnalysis(req.Name)
	// If the caller already passed an operation name, this is a no-op.

	op, err := store.GetByName(ctx, global.GetPgSQLManager(), opName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get analysis: %v", err)
	}
	if op == nil {
		return nil, status.Errorf(codes.NotFound, "analysis %q not found", req.Name)
	}

	return AnalysisFromStore(op), nil
}
