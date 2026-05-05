package nats

// Generate the AsyncAPI specification from Go types.
// The JSON Schema sections are reflected from WorkRequest, ExecutorMessage,
// LogEntry, and CancelRequest in manager.go.
//
// Run with:
//
//	go generate ./pkg/services/nats/...
//
// or from the module root:
//
//	go generate ./...

//go:generate go run ../../../cmd/asyncapi-gen
