package observability

import (
	"github.com/zeromicro/go-zero/core/metric"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
)

// Prometheus metric names used by the idempotency plugin.
const (
	MetricBeginTotal    = "idempotency_begin_total"
	MetricCommitTotal   = "idempotency_commit_total"
	MetricReplayTotal   = "idempotency_replay_total"
	MetricStorageErrors = "idempotency_storage_errors_total"
	MetricWaitSeconds   = "idempotency_wait_seconds"
	MetricRecordBytes   = "idempotency_record_bytes"
)

// GoZeroMetrics adapts go-zero's metric counters and histograms to the
// port.Metrics interface. It uses the global go-zero metric registry,
// which is backed by Prometheus.
type GoZeroMetrics struct {
	counters   map[string]*counterDef
	histograms map[string]*histogramDef
}

type counterDef struct {
	vec    metric.CounterVec
	labels []string
}

type histogramDef struct {
	vec    metric.HistogramVec
	labels []string
}

// NewGoZeroMetrics creates a Metrics backed by go-zero's Prometheus metrics.
func NewGoZeroMetrics() *GoZeroMetrics {
	resultLabels := []string{"result"}
	beginLabels := []string{"result", "result_type"}

	m := &GoZeroMetrics{
		counters: map[string]*counterDef{
			MetricBeginTotal: {
				vec:    metric.NewCounterVec(&metric.CounterVecOpts{Name: MetricBeginTotal, Help: "Total idempotency Begin calls.", Labels: beginLabels}),
				labels: beginLabels,
			},
			MetricCommitTotal: {
				vec:    metric.NewCounterVec(&metric.CounterVecOpts{Name: MetricCommitTotal, Help: "Total idempotency Commit calls.", Labels: resultLabels}),
				labels: resultLabels,
			},
			MetricReplayTotal: {
				vec:    metric.NewCounterVec(&metric.CounterVecOpts{Name: MetricReplayTotal, Help: "Total idempotency Replay calls."}),
				labels: nil,
			},
			MetricStorageErrors: {
				vec:    metric.NewCounterVec(&metric.CounterVecOpts{Name: MetricStorageErrors, Help: "Total idempotency storage errors."}),
				labels: nil,
			},
		},
		histograms: map[string]*histogramDef{
			MetricWaitSeconds: {
				vec: metric.NewHistogramVec(&metric.HistogramVecOpts{
					Name: MetricWaitSeconds, Help: "Wait duration in seconds.",
					Labels: beginLabels, Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
				}),
				labels: beginLabels,
			},
			MetricRecordBytes: {
				vec: metric.NewHistogramVec(&metric.HistogramVecOpts{
					Name: MetricRecordBytes, Help: "Record size in bytes.",
					Labels: resultLabels, Buckets: []float64{256, 512, 1024, 4096, 16384, 65536, 262144},
				}),
				labels: resultLabels,
			},
		},
	}
	return m
}

func (m *GoZeroMetrics) CounterIncrement(name string, labels map[string]string) {
	cd, ok := m.counters[name]
	if !ok {
		return
	}
	args := labelValues(cd.labels, labels)
	cd.vec.Inc(args...)
}

func (m *GoZeroMetrics) HistogramObserve(name string, value float64, labels map[string]string) {
	hd, ok := m.histograms[name]
	if !ok {
		return
	}
	args := labelValues(hd.labels, labels)
	hd.vec.ObserveFloat(value, args...)
}

// labelValues extracts label values in the same order as the label names.
func labelValues(names []string, values map[string]string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = values[name]
	}
	return out
}

var _ port.Metrics = (*GoZeroMetrics)(nil)
