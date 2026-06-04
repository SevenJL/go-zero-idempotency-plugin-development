package observability

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
)

// OTelMetrics adapts the OpenTelemetry Metrics SDK to the port.Metrics
// interface. It lazily creates counters and histograms on first use so
// callers don't need to pre-register metric names.
//
// Use NewOTelMetrics() with the meter name, or NewOTelMetricsWithProvider()
// to supply a custom MeterProvider.
//
// Note: the port.Metrics interface does not accept a context.Context parameter,
// so metric recordings use context.Background(). This means trace context is
// not propagated to metrics, which limits correlation in distributed tracing
// dashboards. A future interface revision should add ctx parameters to
// CounterIncrement and HistogramObserve.
type OTelMetrics struct {
	meter metric.Meter

	mu       sync.RWMutex
	counters map[string]metric.Int64Counter
	hists    map[string]metric.Float64Histogram
}

// NewOTelMetrics creates a Metrics adapter using the OTel global MeterProvider.
// meterName is the instrumentation scope name (e.g. "idempotency").
func NewOTelMetrics(meterName string) *OTelMetrics {
	return NewOTelMetricsWithProvider(otel.GetMeterProvider(), meterName)
}

// NewOTelMetricsWithProvider creates a Metrics adapter using a custom
// MeterProvider. This is the preferred constructor when you are setting up
// an SDK MeterProvider explicitly.
func NewOTelMetricsWithProvider(provider metric.MeterProvider, meterName string) *OTelMetrics {
	return &OTelMetrics{
		meter:    provider.Meter(meterName),
		counters: make(map[string]metric.Int64Counter),
		hists:    make(map[string]metric.Float64Histogram),
	}
}

// CounterIncrement increments the named counter by 1.
func (m *OTelMetrics) CounterIncrement(name string, labels map[string]string) {
	c, err := m.getOrCreateCounter(name)
	if err != nil {
		return
	}
	c.Add(context.Background(), 1, metric.WithAttributes(mapToAttrs(labels)...))
}

// HistogramObserve records a value on the named histogram.
func (m *OTelMetrics) HistogramObserve(name string, value float64, labels map[string]string) {
	h, err := m.getOrCreateHistogram(name)
	if err != nil {
		return
	}
	h.Record(context.Background(), value, metric.WithAttributes(mapToAttrs(labels)...))
}

func (m *OTelMetrics) getOrCreateCounter(name string) (metric.Int64Counter, error) {
	// Fast path: read lock
	m.mu.RLock()
	c, ok := m.counters[name]
	m.mu.RUnlock()
	if ok {
		return c, nil
	}

	// Slow path: create under write lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check — another goroutine may have created it
	if c, ok = m.counters[name]; ok {
		return c, nil
	}

	c, err := m.meter.Int64Counter(name,
		metric.WithDescription(fmt.Sprintf("Count of %s events", name)),
	)
	if err != nil {
		return nil, err
	}
	m.counters[name] = c
	return c, nil
}

func (m *OTelMetrics) getOrCreateHistogram(name string) (metric.Float64Histogram, error) {
	m.mu.RLock()
	h, ok := m.hists[name]
	m.mu.RUnlock()
	if ok {
		return h, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok = m.hists[name]; ok {
		return h, nil
	}

	h, err := m.meter.Float64Histogram(name,
		metric.WithDescription(fmt.Sprintf("Distribution of %s", name)),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}
	m.hists[name] = h
	return h, nil
}

func mapToAttrs(labels map[string]string) []attribute.KeyValue {
	if len(labels) == 0 {
		return nil
	}
	attrs := make([]attribute.KeyValue, 0, len(labels))
	for k, v := range labels {
		attrs = append(attrs, attribute.String(k, v))
	}
	return attrs
}

var _ port.Metrics = (*OTelMetrics)(nil)
