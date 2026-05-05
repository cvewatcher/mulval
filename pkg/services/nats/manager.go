package nats

import (
	"context"
	"fmt"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	apiv1 "github.com/cvewatcher/mulval/api/v1"
	"github.com/cvewatcher/mulval/pkg/monitoring"
)

const (
	TypeLog       = "dev.cvewatcher.mulval.operation.log"
	TypeSucceeded = "dev.cvewatcher.mulval.operation.succeeded"
	TypeFailed    = "dev.cvewatcher.mulval.operation.failed"
)

// Manager wraps a NATS connection and JetStream context.
// It provisions the required streams and KV bucket on first connect
// and re-provisions on reconnect.
type Manager struct {
	config ManagerConfig
	nc     *nats.Conn
	js     jetstream.JetStream
}

type ManagerConfig struct {
	URL        string
	InstanceID string

	Logger  *zap.Logger
	Tracer  trace.Tracer
	Metrics *monitoring.Metrics
}

func NewManager(ctx context.Context, config ManagerConfig) (*Manager, error) {
	man := &Manager{
		config: config,
	}
	if err := man.Connect(ctx); err != nil {
		return nil, err
	}
	return man, nil
}

// Connect establishes the NATS connection and provisions streams.
// Reconnection is handled automatically by the NATS client with callbacks
// that re-provision streams (idempotent) and update metrics.
func (m *Manager) Connect(ctx context.Context) error {
	opts := []nats.Option{
		nats.Name("mulval/" + m.config.InstanceID),
		nats.MaxReconnects(-1), // reconnect forever
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			m.config.Metrics.NATSReconnect.Add(ctx, 1)

			m.config.Logger.Warn("NATS reconnected",
				zap.String("url", nc.ConnectedUrl()),
			)

			// Re-provision streams after reconnect
			if err := m.provisionStreams(ctx); err != nil {
				m.config.Logger.Error("re-provision streams after reconnect", zap.Error(err))
			}
		}),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			m.config.Logger.Warn("NATS disconnected", zap.Error(err))
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			m.config.Logger.Info("NATS connection closed")
		}),
	}

	nc, err := nats.Connect(m.config.URL, opts...)
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}
	m.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("jetstream context: %w", err)
	}
	m.js = js

	if err := m.provisionStreams(ctx); err != nil {
		return err
	}

	m.config.Logger.Info("NATS connected and streams provisioned",
		zap.String("url", m.config.URL),
	)
	return nil
}

func (m *Manager) provisionStreams(ctx context.Context) error {
	if _, err := m.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       apiv1.OperationStream,
		Subjects:   []string{apiv1.OperationSubjectBase + ".*"},
		Retention:  jetstream.InterestPolicy,
		MaxAge:     1 * time.Hour, // events are short-lived - callers consume quickly
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	}); err != nil {
		return fmt.Errorf("provision operation update stream: %w", err)
	}

	// EXECUTOR_CANCEL - persisted cancel signals with short retention.
	// LimitsPolicy (default) retains messages regardless of subscriber presence,
	// so a cancel published before the executor subscribes is still delivered.
	// InterestPolicy would silently drop the message if no subscriber exists yet.
	if _, err := m.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      apiv1.CancelStream,
		Subjects:  []string{apiv1.CancelSubject + ".*"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    10 * time.Minute,
		// One cancel per operation subject is enough - discard old if somehow
		// a second cancel arrives for the same operation.
		MaxMsgsPerSubject: 1,
		Replicas:          1,
	}); err != nil {
		return fmt.Errorf("provision cancel stream: %w", err)
	}

	return nil
}

func (m *Manager) SubscribeOperationUpdate(ctx context.Context, opID string) (<-chan jetstream.Msg, func(), error) {
	subject := apiv1.OperationSubjectBase + "." + opID

	ctx, span := m.config.Tracer.Start(ctx, "NatsManager/SubscribeOperationUpdate",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.String("messaging.nats.stream", subject[:strings.LastIndex(subject, ".")]),
		),
	)

	cons, err := m.js.OrderedConsumer(ctx, apiv1.OperationStream, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		span.RecordError(err)
		span.End()
		return nil, nil, fmt.Errorf("ordered consumer for %s: %w", opID, err)
	}
	ch := make(chan jetstream.Msg, 1) // buffer to only 1

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		select {
		case ch <- msg:
		case <-ctx.Done():
			span.End()
		}
	})
	if err != nil {
		return nil, nil, fmt.Errorf("consume for %s: %w", opID, err)
	}

	stop := func() {
		cc.Stop()
		// Drain remaining messages from the channel so the goroutine
		// writing to it (the JetStream callback) is never blocked.
		for {
			select {
			case <-ch:
			default:
				return
			}
		}
	}

	return ch, stop, nil
}

func (m *Manager) PublishOperationUpdate(
	ctx context.Context,
	opID string,
) error {
	subject := apiv1.OperationSubjectBase + "." + opID

	ctx, span := m.config.Tracer.Start(ctx, "NatsManager/PublishOperationUpdate",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.String("messaging.nats.stream", subject[:strings.LastIndex(subject, ".")]),
		),
	)
	defer span.End()

	ce := m.newCE(apiv1.TypeOperationUpdate, opID)
	if err := ce.SetData(cloudevents.ApplicationJSON, apiv1.OperationUpdate{
		ID: opID,
	}); err != nil {
		return fmt.Errorf("set operation update data: %w", err)
	}

	return m.publishCE(ctx, subject, ce)
}

// SubscribeCancel polls the cancel stream for a cancel message, calling
// handler at most once. Closes automatically when ctx is cancelled.
func (m *Manager) SubscribeCancel(ctx context.Context, opID string, handler func(req apiv1.Cancel)) error {
	subject := apiv1.CancelSubject + "." + opID

	ctx, span := m.config.Tracer.Start(ctx, "NatsManager/SubscribeCancel",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.String("messaging.nats.stream", subject[:strings.LastIndex(subject, ".")]),
		),
	)

	cons, err := m.js.CreateOrUpdateConsumer(ctx, apiv1.CancelStream, jetstream.ConsumerConfig{
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		span.RecordError(err)
		span.End()
		return fmt.Errorf("cancel consumer for %s: %w", opID, err)
	}

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second)) // <0.2 cancel/s is realistic, else is clearly event broker spamming
				if err != nil {
					span.RecordError(err)
					continue
				}
				for msg := range msgs.Messages() {
					msgCtx := otel.GetTextMapPropagator().Extract(ctx, HeaderCarrier(msg.Headers()))

					_, span := m.config.Tracer.Start(msgCtx, "cancel",
						trace.WithSpanKind(trace.SpanKindConsumer),
						trace.WithAttributes(
							attribute.String("messaging.system", "nats"),
							attribute.String("messaging.nats.stream", subject[:strings.LastIndex(subject, ".")]),
							attribute.String("messaging.nats.consumer", "mulval"),
						),
					)

					req, err := UnmarshalCancel(msg.Data())
					if err != nil {
						span.RecordError(err)
						span.End()
						m.config.Logger.Warn("malformed cancel message", zap.Error(err))
						continue
					}

					_ = msg.Ack()
					handler(req)
					span.End()
				}
			}
		}
	}()

	return nil
}

// PublishCancel wraps a CancelRequest in a CloudEvent and publishes it
// to the cancel stream with Nats-Msg-Id deduplication.
func (m *Manager) PublishCancel(ctx context.Context, source, opID, reason string) error {
	ce := cloudevents.NewEvent()
	ce.SetSpecVersion(cloudevents.VersionV1)
	ce.SetID(uuid.NewString())
	ce.SetSource(source)
	ce.SetType(apiv1.TypeCancel)
	ce.SetSubject(opID)
	ce.SetTime(time.Now().UTC())
	ce.SetDataContentType(cloudevents.ApplicationJSON)
	if err := ce.SetData(cloudevents.ApplicationJSON, apiv1.Cancel{
		Operation: opID,
		Reason:    reason,
	}); err != nil {
		return fmt.Errorf("set cancel data: %w", err)
	}
	return m.publishCE(ctx, apiv1.CancelSubject+"."+opID, ce)
}

// Healthcheck verifies the NATS connection and JetStream availability.
// It checks the connection status and performs a lightweight JetStream API call (AccountInfo)to confirm the
// server is reachable and JetStream is enabled.*
// Returns nil if healthy.
func (m *Manager) Healthcheck(ctx context.Context) error {
	if m.nc == nil {
		return fmt.Errorf("nats: not connected")
	}
	if !m.nc.IsConnected() {
		return fmt.Errorf("nats: connection status %s", m.nc.Status())
	}
	// Lightweight JetStream probe.
	// AccountInfo is a single round-trip that confirms both network reachability and JetStream availability.
	if _, err := m.js.AccountInfo(ctx); err != nil {
		return fmt.Errorf("nats: jetstream unavailable: %w", err)
	}
	return nil
}

// Close drains and closes the underlying NATS connection.
func (m *Manager) Close() {
	if m.nc != nil {
		_ = m.nc.Drain()
	}
}
