package analysis

import (
	"encoding/csv"
	"math"
	"strconv"
	"strings"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cvewatcher/mulval/pkg/store"
	analysispb "github.com/cvewatcher/mulval/proto/api/v1/analysis"
)

// toAnalysis converts a store.Operation to its proto representation.
// The resource name is always the analysis form: "analyses/{uuid}".
func AnalysisFromStore(op *store.Operation) *analysispb.Analysis {
	a := &analysispb.Analysis{
		Name:       store.AnalysisNameFromOperation(op.OperationName),
		AnalysisId: store.UUIDFromName(op.OperationName),
		EdbFacts:   strings.Split(op.EDBFacts, "\n"),
		IdbRules:   strings.Split(op.IDBRules, "\n"),
		State:      ToProtoState(op.State),
		CreateTime: timestamppb.New(op.CreateTime),
	}
	if op.EndTime != nil {
		a.EndTime = timestamppb.New(*op.EndTime)
	}
	if op.Error != nil {
		a.Error = *op.Error
	}
	if op.Output != nil {
		a.AttackGraph = &analysispb.AttackGraph{
			VerticesCsv: op.Output.VerticesCSV,
			ArcsCsv:     op.Output.ArcsCSV,
			Summary:     op.Output.Summary,
			Vertices:    parseVerticesCSV(op.Output.VerticesCSV),
			Arcs:        parseArcsCSV(op.Output.ArcsCSV),
		}
	}
	return a
}

// toOperation wraps a store.Operation in an AIP-151 LRO.
// The operation name is always the LRO form: "operations/{uuid}".
// done=true when the operation has reached a terminal state.
func OperationFromStore(op *store.Operation) *longrunningpb.Operation {
	analysis := AnalysisFromStore(op)

	lro := &longrunningpb.Operation{
		Name: op.OperationName,
		Done: op.State.IsTerminal(),
	}

	switch op.State {
	case store.StateSucceeded:
		resp, _ := anypb.New(analysis)
		lro.Result = &longrunningpb.Operation_Response{Response: resp}
	case store.StateFailed:
		errMsg := ""
		if op.Error != nil {
			errMsg = *op.Error
		}
		lro.Result = &longrunningpb.Operation_Error{
			Error: &rpcstatus.Status{
				Code:    int32(codes.Internal),
				Message: errMsg,
			},
		}
	}

	return lro
}

// toProtoState maps a store.State to its analysispb.Analysis_State counterpart.
func ToProtoState(s store.State) analysispb.Analysis_State {
	switch s {
	case store.StateRunning:
		return analysispb.Analysis_RUNNING
	case store.StateSucceeded:
		return analysispb.Analysis_SUCCEEDED
	case store.StateFailed:
		return analysispb.Analysis_FAILED
	case store.StateCancelled:
		return analysispb.Analysis_CANCELLED
	default:
		return analysispb.Analysis_STATE_UNSPECIFIED
	}
}

// ── CSV parsers ───────────────────────────────────────────────────────────────

// parseVerticesCSV parses MulVAL's VERTICES.CSV into proto Vertex messages.
//
// Actual MulVAL format (no header row):
//
//	id,"fact","TYPE",isFact
//
// The goal node is the vertex that appears as a target but never as a source
// in the arc list — i.e. it has no outgoing arcs in MulVAL's directed graph.
// We identify it as the vertex with the lowest ID that has no parent arc,
// which in practice is always vertex 1 (the attackGoal).
func parseVerticesCSV(raw string) []*analysispb.Vertex {
	if raw == "" {
		return nil
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil
	}
	var out []*analysispb.Vertex
	for _, rec := range records {
		if len(rec) < 3 {
			continue
		}
		// Skip optional header row if present.
		if rec[0] == "VertexID" || rec[0] == "\"VertexID\"" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(rec[0]))
		if err != nil {
			continue
		}
		if id > math.MaxInt32 || id < math.MinInt32 {
			// This should not happen (MulVal cannot deal with this scale) so we silently drop
			continue
		}
		fact := strings.Trim(strings.TrimSpace(rec[1]), "")
		vt := toVertexType(strings.Trim(strings.TrimSpace(rec[2]), ""))
		// Column 4 (index 3) is isFact; no dedicated isGoal column.
		// Vertex 1 is always the attackGoal root in MulVAL output.
		isGoal := id == 1
		out = append(out, &analysispb.Vertex{
			Id:         int32(id), //nolint:gosec //#gosec G109 -- boundaries are checked ahead
			Fact:       fact,
			VertexType: vt,
			IsGoal:     isGoal,
		})
	}
	return out
}

// parseArcsCSV parses MulVAL's ARCS.CSV into proto Arc messages.
//
// Actual MulVAL format (no header row):
//
//	src,dst,weight
//
// Weight is always -1 and is ignored.
func parseArcsCSV(raw string) []*analysispb.Arc {
	if raw == "" {
		return nil
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil
	}
	var out []*analysispb.Arc
	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		// Skip optional header row if present.
		if rec[0] == "src" || rec[0] == "\"src\"" {
			continue
		}
		src, err1 := strconv.Atoi(strings.TrimSpace(rec[0]))
		dst, err2 := strconv.Atoi(strings.TrimSpace(rec[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		if src > math.MaxInt32 || src < math.MinInt32 || dst > math.MaxInt32 || dst < math.MinInt32 {
			// This should not happen (MulVal cannot deal with this scale) so we silently drop
			continue
		}
		out = append(out, &analysispb.Arc{
			Src: int32(src), //nolint:gosec //#gosec G109 -- boundaries are checked ahead
			Dst: int32(dst), //nolint:gosec //#gosec G109 -- boundaries are checked ahead
		},
		)
	}
	return out
}

func toVertexType(s string) analysispb.Vertex_VertexType {
	switch strings.ToUpper(s) {
	case "AND":
		return analysispb.Vertex_AND
	case "OR":
		return analysispb.Vertex_OR
	case "LEAF":
		return analysispb.Vertex_LEAF
	default:
		return analysispb.Vertex_VERTEX_TYPE_UNSPECIFIED
	}
}
