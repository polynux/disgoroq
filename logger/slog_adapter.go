package logger

import (
	"context"
	"log/slog"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ZapHandler wraps a zap.Logger to implement slog.Handler
type ZapHandler struct {
	zapLogger *zap.Logger
	attrs     []slog.Attr
	group     string
}

// NewZapHandler creates a new slog.Handler that writes to the given zap.Logger
func NewZapHandler(zapLogger *zap.Logger) *ZapHandler {
	return &ZapHandler{zapLogger: zapLogger}
}

// Enabled returns true if the logger is enabled for the given level
func (h *ZapHandler) Enabled(_ context.Context, level slog.Level) bool {
	zapLevel := convertSlogLevel(level)
	return h.zapLogger.Core().Enabled(zapLevel)
}

// Handle handles the log record
func (h *ZapHandler) Handle(_ context.Context, r slog.Record) error {
	zapLevel := convertSlogLevel(r.Level)

	fields := make([]zap.Field, 0, r.NumAttrs()+len(h.attrs))

	r.Attrs(func(attr slog.Attr) bool {
		fields = append(fields, convertSlogAttr(attr)...)
		return true
	})

	for _, attr := range h.attrs {
		fields = append(fields, convertSlogAttr(attr)...)
	}

	h.zapLogger.Log(zapLevel, r.Message, fields...)
	return nil
}

// WithAttrs returns a new handler with the given attributes
func (h *ZapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandler := *h
	newHandler.attrs = append(newHandler.attrs, attrs...)
	return &newHandler
}

// WithGroup returns a new handler with the given group
func (h *ZapHandler) WithGroup(name string) slog.Handler {
	newHandler := *h
	newHandler.group = name
	return &newHandler
}

func convertSlogLevel(level slog.Level) zapcore.Level {
	switch level {
	case slog.LevelDebug:
		return zapcore.DebugLevel
	case slog.LevelInfo:
		return zapcore.InfoLevel
	case slog.LevelWarn:
		return zapcore.WarnLevel
	case slog.LevelError:
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func convertSlogAttr(attr slog.Attr) []zap.Field {
	key := attr.Key
	value := attr.Value

	switch value.Kind() {
	case slog.KindString:
		return []zap.Field{zap.String(key, value.String())}
	case slog.KindInt64:
		return []zap.Field{zap.Int64(key, value.Int64())}
	case slog.KindFloat64:
		return []zap.Field{zap.Float64(key, value.Float64())}
	case slog.KindBool:
		return []zap.Field{zap.Bool(key, value.Bool())}
	case slog.KindTime:
		return []zap.Field{zap.Time(key, value.Time())}
	case slog.KindDuration:
		return []zap.Field{zap.Duration(key, value.Duration())}
	case slog.KindAny:
		return []zap.Field{zap.Any(key, value.Any())}
	default:
		return []zap.Field{zap.Any(key, value.Any())}
	}
}

// NewSlogLogger creates a slog.Logger that writes to the global Zap logger
func NewSlogLogger() *slog.Logger {
	return slog.New(NewZapHandler(Log))
}
