package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
)

// OTelLogger adapts Go's log/slog (used by OpenTelemetry's log bridge)
// to the port.Logger interface. Structured fields are emitted as slog
// attributes.
//
// Use NewOTelLogger() for a logger that writes JSON to stderr, or
// NewOTelLoggerWithHandler() to provide a custom slog.Handler.
type OTelLogger struct {
	logger *slog.Logger
}

// NewOTelLogger creates an OTel-compatible JSON logger writing to stderr
// at Info level. This is suitable for containerized environments where
// logs are collected from stdout/stderr.
func NewOTelLogger() *OTelLogger {
	return NewOTelLoggerWithHandler(
		slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
}

// NewOTelLoggerWithHandler creates a Logger backed by the given slog.Handler.
// Use this to integrate with OTel's log bridge or a custom handler.
func NewOTelLoggerWithHandler(handler slog.Handler) *OTelLogger {
	return &OTelLogger{logger: slog.New(handler)}
}

// NewOTelLoggerWithLogger creates a Logger backed by an existing *slog.Logger.
func NewOTelLoggerWithLogger(logger *slog.Logger) *OTelLogger {
	return &OTelLogger{logger: logger}
}

func (l *OTelLogger) Info(ctx context.Context, msg string, fields ...port.Field) {
	l.logger.LogAttrs(ctx, slog.LevelInfo, msg, toSlogAttrs(fields)...)
}

func (l *OTelLogger) Error(ctx context.Context, msg string, fields ...port.Field) {
	l.logger.LogAttrs(ctx, slog.LevelError, msg, toSlogAttrs(fields)...)
}

func (l *OTelLogger) Warn(ctx context.Context, msg string, fields ...port.Field) {
	l.logger.LogAttrs(ctx, slog.LevelWarn, msg, toSlogAttrs(fields)...)
}

func toSlogAttrs(fields []port.Field) []slog.Attr {
	attrs := make([]slog.Attr, len(fields))
	for i, f := range fields {
		attrs[i] = slog.Attr{Key: f.Key, Value: slogAnyValue(f.Value)}
	}
	return attrs
}

func slogAnyValue(v any) slog.Value {
	switch val := v.(type) {
	case string:
		return slog.StringValue(val)
	case int:
		return slog.IntValue(val)
	case int64:
		return slog.Int64Value(val)
	case float64:
		return slog.Float64Value(val)
	case bool:
		return slog.BoolValue(val)
	case error:
		return slog.StringValue(val.Error())
	case fmt.Stringer:
		return slog.StringValue(val.String())
	default:
		return slog.StringValue(fmt.Sprintf("%v", val))
	}
}

var _ port.Logger = (*OTelLogger)(nil)
