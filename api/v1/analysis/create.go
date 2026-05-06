package analysis

import (
	"context"
	"strings"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cvewatcher/mulval/global"
	"github.com/cvewatcher/mulval/pkg/store"
	analysispb "github.com/cvewatcher/mulval/proto/api/v1/analysis"
)

const (
	defaultPageSize = 50
	maxPageSize     = 1000
)

// CreateAnalysis implements the AIP-133 Create + AIP-151 LRO pattern.
//
// Flow:
//  1. Validate the request.
//  2. Check the input-hash cache — return existing SUCCEEDED operation if hit.
//  3. Resolve the operation name (client-provided or server-assigned).
//  4. If the operation name already exists, return the existing operation.
//  5. Insert the row as RUNNING and fire the executor goroutine.
//  6. Return the LRO immediately (done=false if still running).
func (a *Analyzer) CreateAnalysis(
	ctx context.Context,
	req *analysispb.CreateAnalysisRequest,
) (*longrunningpb.Operation, error) {
	if len(req.EdbFacts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "analysis.edb_facts must not be empty")
	}

	edb := strings.Join(req.EdbFacts, "\n")
	idb := strings.Join(req.IdbRules, "\n")

	// ── Cache lookup ─────────────────────────────────────────────────────────
	// If an identical set of inputs already produced a SUCCEEDED analysis,
	// return it immediately without launching MulVAL again.
	hash := store.HashInputs(edb, idb)
	if cached, err := store.GetByHash(ctx, global.GetPgSQLManager(), hash); err != nil {
		return nil, status.Errorf(codes.Internal, "cache lookup: %v", err)
	} else if cached != nil {
		return OperationFromStore(cached), nil
	}

	// ── Resolve operation name ────────────────────────────────────────────────
	// AIP-151: callers may supply an idempotency key via analysis_id.
	// If omitted, generate a new UUID-based operation name.
	var opName string
	if req.AnalysisId != "" {
		opName = "operations/" + req.AnalysisId
	} else {
		opName = store.NewOperationName()
	}

	// ── Create or retrieve the analysis row ───────────────────────────────────
	op, created, err := store.CreateAnalysis(ctx, global.GetPgSQLManager(), opName, edb, idb)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create analysis: %v", err)
	}

	// If the row already existed (idempotent retry), return it as-is.
	if !created {
		return OperationFromStore(op), nil
	}

	// ── Launch executor ───────────────────────────────────────────────────────
	if err := global.GetExecutor().Run(ctx, op); err != nil {
		// The row is now orphaned in RUNNING state — mark it failed so the
		// client does not wait indefinitely.
		if _, mErr := store.MarkFailed(ctx, global.GetPgSQLManager(), op, err.Error()); mErr != nil {
			// Best-effort: log via the error return.
			return nil, status.Errorf(codes.Internal,
				"executor start failed (%v) and cleanup also failed (%v)", err, mErr)
		}
		return nil, status.Errorf(codes.Internal, "start executor: %v", err)
	}

	return OperationFromStore(op), nil
}
