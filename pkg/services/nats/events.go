package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	apiv1 "github.com/cvewatcher/mulval/api/v1"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// newCE constructs a CloudEvent using the SDK, sourced from this executor instance.
func (m *Manager) newCE(eventType, subject string) cloudevents.Event {
	e := cloudevents.NewEvent()
	e.SetSpecVersion(cloudevents.VersionV1)
	e.SetID(uuid.NewString())
	e.SetSource(strings.Join([]string{"urn:cvewatcher:mulval", m.config.InstanceID}, ":"))
	e.SetType(eventType)
	e.SetSubject(subject)
	e.SetTime(time.Now().UTC())
	e.SetDataContentType(cloudevents.ApplicationJSON)
	return e
}

// publishCE marshals a CloudEvent and publishes it with Nats-Msg-Id dedup header.
func (m *Manager) publishCE(ctx context.Context, subject string, ce cloudevents.Event) error {
	data, err := json.Marshal(ce)
	if err != nil {
		return fmt.Errorf("marshal cloudevent: %w", err)
	}

	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  nats.Header{},
	}

	// Inject OpenTelemetry trace context into the NATS message headers
	otel.GetTextMapPropagator().Inject(ctx, HeaderCarrier(msg.Header))

	// Nats-Msg-Id enables JetStream server-side deduplication within the
	// stream's Duplicates window (2min). The CloudEvent ID is stable across
	// retries from the same call site, making publish idempotent.
	msg.Header.Set("Nats-Msg-Id", ce.ID())

	span := trace.SpanFromContext(ctx)
	ack, err := m.js.PublishMsg(ctx, msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "jetstream publish failed")
		return fmt.Errorf("publish %s: %w", subject, err)
	}

	span.SetAttributes(
		attribute.Int64("messaging.nats.sequence", int64(ack.Sequence)),
	)
	return nil
}

func UnmarshalCancel(raw []byte) (apiv1.Cancel, error) {
	var cancel apiv1.Cancel
	err := unmarshalCE(raw, apiv1.TypeCancel, &cancel)
	return cancel, err
}

func unmarshalCE(raw []byte, expectedType string, dst any) error {
	var ce cloudevents.Event
	if err := json.Unmarshal(raw, &ce); err != nil {
		return fmt.Errorf("unmarshal cloudevent: %w", err)
	}
	if expectedType != "" && ce.Type() != expectedType {
		return fmt.Errorf("validating cloud event type: got %s", ce.Type())
	}
	return json.Unmarshal(ce.Data(), dst)
}
