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
//
// Context-aware variants (CounterIncrementContext, HistogramObserveContext)
// propagate trace context to metrics for exemplar support. The legacy
// no-context methods call the context variants with context.Background()
// and remain for backward compatibility.
type Metrics interface {
	// CounterIncrement increments the named counter by 1.
	// Deprecated: prefer CounterIncrementContext to propagate trace context.
	CounterIncrement(name string, labels map[string]string)

	// HistogramObserve records a value on the named histogram.
	// Deprecated: prefer HistogramObserveContext to propagate trace context.
	HistogramObserve(name string, value float64, labels map[string]string)

	// CounterIncrementContext increments the named counter by 1, propagating
	// trace context for metric-to-trace correlation (exemplar support).
	CounterIncrementContext(ctx context.Context, name string, labels map[string]string)

	// HistogramObserveContext records a value on the named histogram,
	// propagating trace context for metric-to-trace correlation.
	HistogramObserveContext(ctx context.Context, name string, value float64, labels map[string]string)
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

func (noopMetrics) CounterIncrement(_ string, _ map[string]string)                             {}
func (noopMetrics) HistogramObserve(_ string, _ float64, _ map[string]string)                  {}
func (noopMetrics) CounterIncrementContext(_ context.Context, _ string, _ map[string]string)    {}
func (noopMetrics) HistogramObserveContext(_ context.Context, _ string, _ float64, _ map[string]string) {}

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

// ---------------------------------------------------------------------------
// Notifier port — real-time state transition events
// ---------------------------------------------------------------------------

// Notifier publishes and subscribes to state-transition events, enabling
// sub-millisecond WaitReplay instead of 50ms polling. Implementations
// may use Redis Pub/Sub, NATS, Kafka, or any message bus.
type Notifier interface {
	// Notify publishes a state-transition event to the given channel.
	Notify(ctx context.Context, channel, message string) error

	// Wait subscribes to a channel and blocks until a message arrives
	// or the context is cancelled. Returns the message payload.
	Wait(ctx context.Context, channel string) (string, error)
}

type noopNotifier struct{}

func (noopNotifier) Notify(_ context.Context, _, _ string) error        { return nil }
func (noopNotifier) Wait(_ context.Context, _ string) (string, error)   { return "", nil }

// NoopNotifier returns a Notifier that is a no-op. The service falls back to
// polling when this is used.
func NoopNotifier() Notifier { return noopNotifier{} }
