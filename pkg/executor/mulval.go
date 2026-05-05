package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cvewatcher/mulval/pkg/store"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Run launches a MulVAL analysis as a background goroutine and returns
// immediately once the operation row is created. All results flow through
// PostgreSQL (via the store layer) and NATS.
//
// op must have been created by the caller via store.CreateAnalysis — the
// executor does not insert the initial row itself, allowing the API handler
// to return the LRO synchronously before the goroutine starts.
//
// The returned error covers only the synchronous validation phase. Subprocess
// failures are reported asynchronously via store.MarkFailed + NATS publish.
func (e *Executor) Run(ctx context.Context, op *store.Operation) error {
	ctx, span := e.config.Tracer.Start(ctx, "Executor/Run",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("mulval.operation", op.OperationName)),
	)
	defer span.End()

	if strings.TrimSpace(op.EDBFacts) == "" {
		err := fmt.Errorf("edb_facts must not be empty")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	rc := NewRunContext(RunContextConfig{
		RootCtx:     e.rootCtx,
		ReqCtx:      ctx,
		OpName:      op.OperationName,
		NatsManager: e.config.NatsManager,
		Logger:      e.config.Logger,
		Metrics:     e.config.Metrics,
		Tracer:      e.config.Tracer,
	})

	e.config.Logger.Info("launching executor goroutine",
		zap.String("operation", op.OperationName),
	)

	e.wg.Add(1)
	go e.run(rc, op)

	return nil
}

// run is the goroutine body. It owns the RunContext lifecycle and is
// responsible for all terminal state transitions.
func (e *Executor) run(rc *RunContext, op *store.Operation) {
	defer e.wg.Done()
	defer rc.Finish()

	_, span := e.config.Tracer.Start(rc.Abort, "Executor/run",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("mulval.operation", op.OperationName)),
	)
	defer span.End()

	log := e.config.Logger.With(zap.String("operation", op.OperationName))
	log.Info("executor goroutine started")

	output, err := e.runMulval(rc, op)
	if err != nil {
		// Distinguish explicit cancellation from a genuine subprocess failure.
		if rc.Abort.Err() != nil {
			reason := rc.Reason()
			if reason == "" {
				reason = "operation cancelled"
			}
			log.Info("analysis cancelled", zap.String("reason", reason))
			span.SetStatus(codes.Ok, "cancelled")
			// The CancelOperation RPC already wrote CANCELLED to the DB.
			// Only notify waiters via NATS.
			_ = e.config.NatsManager.PublishOperationUpdate(rc.Store, op.OperationName)
			return
		}

		log.Error("analysis failed", zap.Error(err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		if _, dbErr := store.MarkFailed(rc.Store, e.config.PgsqlManager, op, err.Error()); dbErr != nil {
			log.Error("failed to persist failure state", zap.Error(dbErr))
			span.RecordError(dbErr)
		}
		_ = e.config.NatsManager.PublishOperationUpdate(rc.Store, op.OperationName)
		return
	}

	log.Info("analysis succeeded",
		zap.Int("vertices_bytes", len(output.VerticesCSV)),
		zap.Int("arcs_bytes", len(output.ArcsCSV)),
	)
	span.SetStatus(codes.Ok, "succeeded")

	if _, dbErr := store.MarkSucceeded(
		rc.Store, e.config.PgsqlManager, op,
		&store.OperationOutput{
			VerticesCSV: output.VerticesCSV,
			ArcsCSV:     output.ArcsCSV,
			Summary:     output.Summary,
		},
	); dbErr != nil {
		log.Error("failed to persist success state", zap.Error(dbErr))
		span.RecordError(dbErr)
	}
	_ = e.config.NatsManager.PublishOperationUpdate(rc.Store, op.OperationName)
}

// mulvalOutput holds the raw file outputs from a successful MulVAL run.
type mulvalOutput struct {
	VerticesCSV string
	ArcsCSV     string
	Summary     string
}

// runMulval creates a temp working directory, writes the input files, invokes
// graph_gen.sh, reads the outputs, and cleans up. It respects rc.Abort:
// if cancelled, exec.CommandContext kills the subprocess automatically.
func (e *Executor) runMulval(rc *RunContext, op *store.Operation) (*mulvalOutput, error) {
	_, span := e.config.Tracer.Start(rc.Abort, "Executor/runMulval",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("mulval.operation", op.OperationName)),
	)
	defer span.End()

	// OperationName is "operations/{uuid}" — strip the prefix so the temp
	// dir pattern contains no path separator, which os.MkdirTemp rejects.
	opID := store.UUIDFromName(op.OperationName)
	workDir, err := os.MkdirTemp("", "mulval-"+opID+"-")
	if err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Write input.P — EDB facts.
	if err := os.WriteFile(
		filepath.Join(workDir, "input.P"),
		[]byte(op.EDBFacts+"\n"),
		0o644,
	); err != nil {
		return nil, fmt.Errorf("write input.P: %w", err)
	}

	// graph_gen.sh: flags first, input file last.
	args := []string{"-l"}
	if strings.TrimSpace(op.IDBRules) != "" {
		rulesPath := filepath.Join(workDir, "extra_rules.P")
		if err := os.WriteFile(rulesPath, []byte(op.IDBRules+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("write extra_rules.P: %w", err)
		}
		args = append(args, "-a", "extra_rules.P")
	}
	args = append(args, "input.P")

	// exec.CommandContext kills the process when rc.Abort is cancelled.
	cmd := exec.CommandContext(rc.Abort, "graph_gen.sh", args...)
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		// If rc.Abort was cancelled the caller checks rc.Abort.Err().
		if rc.Abort.Err() != nil {
			return nil, fmt.Errorf("graph_gen.sh: %w\noutput:\n%s", err, string(out))
		}
		// "No attack paths found" is a legitimate MulVAL result — it exits
		// with status 1 even on success when the goal is unreachable.
		// Treat it as a succeeded run with empty outputs rather than a failure.
		if strings.Contains(string(out), "No attack paths found") {
			return &mulvalOutput{
				VerticesCSV: "",
				ArcsCSV:     "",
				Summary:     strings.TrimSpace(string(out)),
			}, nil
		}
		// Append xsb_log.txt if present — it contains the XSB Prolog
		// error details that graph_gen.sh asks you to check.
		xsbLog, _ := os.ReadFile(filepath.Join(workDir, "xsb_log.txt"))
		if len(xsbLog) > 0 {
			return nil, fmt.Errorf("graph_gen.sh: %w\noutput:\n%s\nxsb_log.txt:\n%s",
				err, string(out), string(xsbLog))
		}
		return nil, fmt.Errorf("graph_gen.sh: %w\noutput:\n%s", err, string(out))
	}

	return readOutputs(workDir)
}

// readOutputs reads the three MulVAL output files from workDir.
// VERTICES.CSV and ARCS.CSV are required; AttackGraph.txt is best-effort.
func readOutputs(workDir string) (*mulvalOutput, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(workDir, name))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		return string(b), nil
	}

	vertices, err := read("VERTICES.CSV")
	if err != nil {
		return nil, err
	}
	arcs, err := read("ARCS.CSV")
	if err != nil {
		return nil, err
	}
	summary, _ := read("AttackGraph.txt") // best-effort

	return &mulvalOutput{
		VerticesCSV: vertices,
		ArcsCSV:     arcs,
		Summary:     summary,
	}, nil
}
