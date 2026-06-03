// Package observability provides OpenTelemetry adapters for the idempotency
// plugin's observability ports.
//
// These adapters bridge the port.Logger, port.Metrics, and port.Tracer
// interfaces to the OpenTelemetry Go SDK. They are designed to work with
// any OTel-compatible backend (Jaeger, Zipkin, OTLP, etc.).
//
// Usage:
//
//	import (
//	    "go.opentelemetry.io/otel"
//	    "go.opentelemetry.io/otel/sdk/metric"
//	    oidem "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/observability"
//	)
//
//	// Use the global providers:
//	logger  := oidem.NewOTelLogger()
//	metrics := oidem.NewOTelMetrics(otel.Meter("idempotency"))
//	tracer  := oidem.NewOTelTracer(otel.Tracer("idempotency"))
//
//	// Or wire explicitly:
//	logger  := oidem.NewOTelLoggerWithProvider(logProvider)
//	metrics := oidem.NewOTelMetricsWithProvider(meterProvider, "idempotency")
//	tracer  := oidem.NewOTelTracerWithProvider(traceProvider, "idempotency")
//
// The OTel dependencies are already present through go-zero's transitive
// imports — no additional go.mod entries are required.
package observability
