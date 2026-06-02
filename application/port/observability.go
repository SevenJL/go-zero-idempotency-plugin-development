// Package port defines interfaces that the application layer depends on.
// Concrete implementations live in the infrastructure layer.
package port

import "context"

// Logger is a structured logging port. Implementations must be concurrency-safe.
type Logger interface {
	Info(ctx context.Context, msg string, fields ...Field)
	Error(ctx context.Context, msg string, fields ...Field)
	Warn(ctx context.Context, msg string, fields ...Field)
}

// Field is a key-value pair for structured logging.
type Field struct {
	Key   string
	Value any
}

// Metrics is a metrics port for counters and histograms.
// Metric names follow the idempotency_* convention defined in the
// development documentation.
type Metrics interface {
	CounterIncrement(name string, labels map[string]string)
	HistogramObserve(name string, value float64, labels map[string]string)
}

// Tracer is a distributed tracing port.
type Tracer interface {
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Span represents a single trace span.
type Span interface {
	SetAttributes(attrs ...Attribute)
	End()
}

// Attribute is a key-value pair for span tags.
type Attribute struct {
	Key   string
	Value string
}

// ---------------------------------------------------------------------------
// No-op implementations — used as safe defaults so the application layer
// never needs to check for nil.
// ---------------------------------------------------------------------------

type noopLogger struct{}

func (noopLogger) Info(_ context.Context, _ string, _ ...Field)  {}
func (noopLogger) Error(_ context.Context, _ string, _ ...Field) {}
func (noopLogger) Warn(_ context.Context, _ string, _ ...Field)  {}

type noopMetrics struct{}

func (noopMetrics) CounterIncrement(_ string, _ map[string]string)            {}
func (noopMetrics) HistogramObserve(_ string, _ float64, _ map[string]string) {}

type noopTracer struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) SetAttributes(_ ...Attribute) {}
func (noopSpan) End()                         {}

// NoopLogger returns a Logger that discards all messages.
func NoopLogger() Logger { return noopLogger{} }

// NoopMetrics returns a Metrics that discards all observations.
func NoopMetrics() Metrics { return noopMetrics{} }

// NoopTracer returns a Tracer that creates no-op spans.
func NoopTracer() Tracer { return noopTracer{} }
