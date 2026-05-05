package operations

import (
	"context"
	"strings"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cvewatcher/mulval/api/v1/analysis"
	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
)

const (
	defaultListPageSize = 50
	maxListPageSize     = 1000
)

// ListOperations returns a paginated list of operations.
//
// AIP-151 defines a string filter field. This implementation supports
// a simple equality filter on the state field:
//
//	filter: "state=RUNNING"
//	filter: "state=SUCCEEDED"
//	filter: "state=FAILED"
//	filter: "state=CANCELLED"
//
// An empty or unrecognised filter returns all operations.
// Results are ordered newest-first (create_time DESC), consistent with
// ListAnalyses. The same cursor-based page token is used.
func (s *OperationServer) ListOperations(
	ctx context.Context,
	req *longrunningpb.ListOperationsRequest,
) (*longrunningpb.ListOperationsResponse, error) {
	pageSize := int(req.PageSize)
	switch {
	case pageSize <= 0:
		pageSize = defaultListPageSize
	case pageSize > maxListPageSize:
		pageSize = maxListPageSize
	}

	// Parse the optional state filter.
	stateFilter := parseStateFilter(req.Filter)

	result, err := store.ListAnalyses(ctx, global.GetPgSQLManager(), pageSize, req.PageToken)
	if err != nil {
		if isInvalidToken(err) {
			return nil, status.Errorf(codes.InvalidArgument,
				"invalid page_token: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "list operations: %v", err)
	}

	ops := make([]*longrunningpb.Operation, 0, len(result.Operations))
	for _, op := range result.Operations {
		// Apply state filter in-process. For the PoC result sets are small;
		// a production implementation would push the filter into the SQL query.
		if stateFilter != "" && string(op.State) != stateFilter {
			continue
		}
		ops = append(ops, analysis.OperationFromStore(op))
	}

	return &longrunningpb.ListOperationsResponse{
		Operations:    ops,
		NextPageToken: result.NextPageToken,
	}, nil
}

// parseStateFilter extracts the state value from a filter string of the form
// "state=<VALUE>". Returns empty string if the filter is absent or malformed.
func parseStateFilter(filter string) string {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return ""
	}
	const prefix = "state="
	if !strings.HasPrefix(strings.ToLower(filter), prefix) {
		return ""
	}
	val := strings.TrimPrefix(filter, prefix)
	val = strings.TrimPrefix(val, strings.ToLower(prefix))
	return strings.ToUpper(strings.TrimSpace(val))
}

// isInvalidToken mirrors the check in the analysis package.
func isInvalidToken(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid page token") ||
		strings.Contains(msg, "malformed page token") ||
		strings.Contains(msg, "invalid page token timestamp")
}
