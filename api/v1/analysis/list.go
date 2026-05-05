package analysis

import (
	"context"

	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
	analysispb "github.com/cvewatcher/mulval/proto/api/v1/analysis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListAnalyses implements AIP-132 with keyset cursor pagination.
//
// Results are ordered by (create_time DESC, operation_name), which:
//   - Returns the newest analyses first, matching typical operator usage.
//   - Provides a stable cursor: new rows inserted between pages only appear
//     on page 1, never shift existing pages, enabling downstream services
//     to use the first-page delta for drift detection.
//
// Page size defaults to 50 and is capped at 1000.
// Pass NextPageToken from the previous response to advance to the next page.
func (a *Analyzer) ListAnalyses(
	ctx context.Context,
	req *analysispb.ListAnalysesRequest,
) (*analysispb.ListAnalysesResponse, error) {
	pageSize := int(req.PageSize)
	switch {
	case pageSize <= 0:
		pageSize = defaultPageSize
	case pageSize > maxPageSize:
		pageSize = maxPageSize
	}

	result, err := store.ListAnalyses(ctx, global.GetPgSQLManager(), pageSize, req.PageToken)
	if err != nil {
		// Distinguish malformed token (client error) from DB error (internal).
		if isInvalidToken(err) {
			return nil, status.Errorf(codes.InvalidArgument,
				"invalid page_token: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "list analyses: %v", err)
	}

	analyses := make([]*analysispb.Analysis, 0, len(result.Operations))
	for _, op := range result.Operations {
		analyses = append(analyses, AnalysisFromStore(op))
	}

	return &analysispb.ListAnalysesResponse{
		Analyses:      analyses,
		NextPageToken: result.NextPageToken,
	}, nil
}

// isInvalidToken reports whether err originates from cursor decoding.
func isInvalidToken(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "invalid page token") ||
		contains(msg, "malformed page token") ||
		contains(msg, "invalid page token timestamp")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
