package nats

import "github.com/nats-io/nats.go"

// From https://oneuptime.com/blog/post/2026-02-06-trace-nats-message-streams-opentelemetry/view

// HeaderCarrier adapts nats.Header to the TextMapCarrier interface
// so OpenTelemetry can inject and extract trace context
type HeaderCarrier nats.Header

func (c HeaderCarrier) Get(key string) string {
	return nats.Header(c).Get(key)
}

func (c HeaderCarrier) Set(key, value string) {
	nats.Header(c).Set(key, value)
}

func (c HeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
