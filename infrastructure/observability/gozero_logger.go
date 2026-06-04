package observability

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
)

// GoZeroLogger adapts go-zero's logx to the port.Logger interface.
// Structured fields are logged via logx.WithFields on the context, then
// emitted through logx.Infow / Errorw / Sloww.
type GoZeroLogger struct{}

// NewGoZeroLogger creates a Logger backed by go-zero's logx.
func NewGoZeroLogger() *GoZeroLogger {
	return &GoZeroLogger{}
}

func (l *GoZeroLogger) Info(ctx context.Context, msg string, fields ...port.Field) {
	ctx = logx.WithFields(ctx, toLogxFields(fields)...)
	logx.WithContext(ctx).Infow(msg)
}

func (l *GoZeroLogger) Error(ctx context.Context, msg string, fields ...port.Field) {
	ctx = logx.WithFields(ctx, toLogxFields(fields)...)
	logx.WithContext(ctx).Errorw(msg)
}

func (l *GoZeroLogger) Warn(ctx context.Context, msg string, fields ...port.Field) {
	// go-zero does not have a native WARN level. We emit through Errorw
	// with an explicit level tag so operators can filter in log aggregation.
	fields = append(fields, port.Field{Key: "level", Value: "warn"})
	ctx = logx.WithFields(ctx, toLogxFields(fields)...)
	logx.WithContext(ctx).Errorw(msg)
}

func toLogxFields(fields []port.Field) []logx.LogField {
	out := make([]logx.LogField, len(fields))
	for i, f := range fields {
		out[i] = logx.Field(f.Key, f.Value)
	}
	return out
}

var _ port.Logger = (*GoZeroLogger)(nil)
