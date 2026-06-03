package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
)

// OTelTracer adapts the OpenTelemetry Tracing SDK to the port.Tracer
// interface. It creates real OTel spans that integrate with distributed
// tracing backends (Jaeger, Zipkin, OTLP exporters, etc.).
//
// Use NewOTelTracer() for the global TracerProvider, or
// NewOTelTracerWithProvider() to supply a custom one.
type OTelTracer struct {
	tracer trace.Tracer
}

// NewOTelTracer creates a Tracer adapter using the OTel global TracerProvider.
// tracerName is the instrumentation scope (e.g. "idempotency").
func NewOTelTracer(tracerName string) *OTelTracer {
	return NewOTelTracerWithProvider(otel.GetTracerProvider(), tracerName)
}

// NewOTelTracerWithProvider creates a Tracer adapter using a custom
// TracerProvider.
func NewOTelTracerWithProvider(provider trace.TracerProvider, tracerName string) *OTelTracer {
	return &OTelTracer{tracer: provider.Tracer(tracerName)}
}

func (t *OTelTracer) StartSpan(ctx context.Context, name string) (context.Context, port.Span) {
	ctx, span := t.tracer.Start(ctx, name)
	return ctx, &otelSpanWrapper{span: span}
}

// otelSpanWrapper adapts trace.Span to port.Span.
type otelSpanWrapper struct {
	span trace.Span
}

func (s *otelSpanWrapper) SetAttributes(attrs ...port.Attribute) {
	otelAttrs := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		otelAttrs[i] = attribute.String(a.Key, a.Value)
	}
	s.span.SetAttributes(otelAttrs...)
}

func (s *otelSpanWrapper) End() {
	s.span.End()
}

// ---- Extensions beyond the port ----

// RecordError records an error on the span and sets its status to Error.
// Use this from middleware adapters when the idempotency operation fails.
func (s *otelSpanWrapper) RecordError(err error) {
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
}

var _ port.Tracer = (*OTelTracer)(nil)
