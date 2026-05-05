package monitoring

import "go.opentelemetry.io/otel/metric"

const (
	// Operations
	OperationsStarted   = "mulval.operations.started"   // counter
	OperationsCompleted = "mulval.operations.completed" // counter, attrs: status, type
	OperationDuration   = "mulval.operations.duration"  // histogram, attrs: type, status

	// Concurrency
	OperationsActive = "mulval.operations.active" // updowncounter

	// NATS
	NATSReconnect = "mulval.nats.reconnect" // counter

	// Dependencies
	PostgreSQLUp = "mulval.pgsql.up"
)

// Metrics holds all initialized instruments.
// Constructed once at startup via New() and passed to wherever needed,
// or accessed via a global if your pattern requires it.
type Metrics struct {
	// Operations
	OperationsStarted   metric.Int64Counter
	OperationsCompleted metric.Int64Counter
	OperationDuration   metric.Float64Histogram

	// Concurrency
	OperationsActive metric.Int64UpDownCounter

	// NATS
	NATSReconnect metric.Int64Counter

	// Dependencies
	PostgreSQLUp metric.Int64ObservableGauge
}

func NewMetrics(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{}
	var err error

	// Operations
	m.OperationsStarted, err = meter.Int64Counter(
		OperationsStarted,
		metric.WithDescription("Total operations started."),
	)
	if err != nil {
		return nil, err
	}

	m.OperationsCompleted, err = meter.Int64Counter(
		OperationsCompleted,
		metric.WithDescription("Total operations completed, by type and final status."),
	)
	if err != nil {
		return nil, err
	}

	m.OperationDuration, err = meter.Float64Histogram(
		OperationDuration,
		metric.WithDescription("Duration of completed operations in seconds."),
		metric.WithUnit("s"),
		// Buckets tuned to Pulumi operation reality: fast ops ~5s, slow ~30min.
		metric.WithExplicitBucketBoundaries(
			5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600,
		),
	)
	if err != nil {
		return nil, err
	}

	// Concurrency
	m.OperationsActive, err = meter.Int64UpDownCounter(
		OperationsActive,
		metric.WithDescription("Number of operations currently running."),
	)
	if err != nil {
		return nil, err
	}

	// NATS
	m.NATSReconnect, err = meter.Int64Counter(
		NATSReconnect,
		metric.WithDescription("Total NATS reconnections."),
	)
	if err != nil {
		return nil, err
	}

	// Dependencies
	m.PostgreSQLUp, err = meter.Int64ObservableGauge(
		PostgreSQLUp,
		metric.WithDescription("1 if PostgreSQL is reachable, 0 otherwise."),
	)
	if err != nil {
		return nil, err
	}

	return m, nil
}
