package logger

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
)

func TestNewZapHandler(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "creates handler with nil attributes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testLogger := zaptest.NewLogger(t)
			handler := NewZapHandler(testLogger)

			assert.NotNil(t, handler)
			assert.NotNil(t, handler.zapLogger)
		})
	}
}

func TestZapHandler_Enabled(t *testing.T) {
	tests := []struct {
		name         string
		zapLevel     zapcore.Level
		slogLevel    slog.Level
		shouldEnable bool
	}{
		{
			name:         "debug level enabled when logger is debug",
			zapLevel:     zapcore.DebugLevel,
			slogLevel:    slog.LevelDebug,
			shouldEnable: true,
		},
		{
			name:         "info level enabled when logger is info",
			zapLevel:     zapcore.InfoLevel,
			slogLevel:    slog.LevelInfo,
			shouldEnable: true,
		},
		{
			name:         "debug disabled when logger is info",
			zapLevel:     zapcore.InfoLevel,
			slogLevel:    slog.LevelDebug,
			shouldEnable: false,
		},
		{
			name:         "warn enabled when logger is warn",
			zapLevel:     zapcore.WarnLevel,
			slogLevel:    slog.LevelWarn,
			shouldEnable: true,
		},
		{
			name:         "error enabled when logger is error",
			zapLevel:     zapcore.ErrorLevel,
			slogLevel:    slog.LevelError,
			shouldEnable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := zap.NewDevelopmentConfig()
			config.Level = zap.NewAtomicLevelAt(tt.zapLevel)
			logger, err := config.Build()
			if err != nil {
				t.Fatalf("failed to build logger: %v", err)
			}

			handler := NewZapHandler(logger)
			enabled := handler.Enabled(context.Background(), tt.slogLevel)
			assert.Equal(t, tt.shouldEnable, enabled)
		})
	}
}

func TestZapHandler_Handle(t *testing.T) {
	tests := []struct {
		name    string
		level   slog.Level
		message string
		wantErr bool
	}{
		{
			name:    "handle info message",
			level:   slog.LevelInfo,
			message: "test message",
			wantErr: false,
		},
		{
			name:    "handle debug message",
			level:   slog.LevelDebug,
			message: "debug message",
		},
		{
			name:    "handle warn message",
			level:   slog.LevelWarn,
			message: "warning message",
		},
		{
			name:    "handle error message",
			level:   slog.LevelError,
			message: "error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t)
			handler := NewZapHandler(logger)
			record := slog.NewRecord(time.Now(), tt.level, tt.message, 0)
			err := handler.Handle(context.Background(), record)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestZapHandler_WithAttrs(t *testing.T) {
	tests := []struct {
		name string
		attr slog.Attr
	}{
		{
			name: "string attr",
			attr: slog.String("key", "value"),
		},
		{
			name: "int attr",
			attr: slog.Int("count", 42),
		},
		{
			name: "bool attr",
			attr: slog.Bool("enabled", true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t)
			handler := NewZapHandler(logger)
			newHandler := handler.WithAttrs([]slog.Attr{tt.attr})
			assert.NotNil(t, newHandler, "WithAttrs should return non-nil handler")
			assert.IsType(t, &ZapHandler{}, newHandler, "WithAttrs should return *ZapHandler")
		})
	}
}

func TestZapHandler_WithGroup(t *testing.T) {
	tests := []struct {
		name      string
		groupName string
	}{
		{
			name:      "empty group",
			groupName: "",
		},
		{
			name:      "named group",
			groupName: "testGroup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t)
			handler := NewZapHandler(logger)
			newHandler := handler.WithGroup(tt.groupName)
			assert.NotNil(t, newHandler, "WithGroup should return non-nil handler")
			assert.IsType(t, &ZapHandler{}, newHandler, "WithGroup should return *ZapHandler")
		})
	}
}

func TestNewSlogLogger(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "creates slog logger from global zap logger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize global logger for test
			cfg := GetConfig()
			if cfg.Enabled {
				logger := NewSlogLogger()
				assert.NotNil(t, logger, "NewSlogLogger should return non-nil logger")
			}
		})
	}
}

func TestConvertSlogLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    slog.Level
		expected zapcore.Level
	}{
		{
			name:     "debug level",
			level:    slog.LevelDebug,
			expected: zapcore.DebugLevel,
		},
		{
			name:     "info level",
			level:    slog.LevelInfo,
			expected: zapcore.InfoLevel,
		},
		{
			name:     "warn level",
			level:    slog.LevelWarn,
			expected: zapcore.WarnLevel,
		},
		{
			name:     "error level",
			level:    slog.LevelError,
			expected: zapcore.ErrorLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertSlogLevel(tt.level)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertSlogAttr(t *testing.T) {
	tests := []struct {
		name    string
		attr    slog.Attr
		wantLen int
	}{
		{
			name:    "string attr",
			attr:    slog.String("key", "value"),
			wantLen: 1,
		},
		{
			name:    "int64 attr",
			attr:    slog.Int64("key", 123),
			wantLen: 1,
		},
		{
			name:    "float64 attr",
			attr:    slog.Float64("key", 3.14),
			wantLen: 1,
		},
		{
			name:    "bool attr",
			attr:    slog.Bool("key", true),
			wantLen: 1,
		},
		{
			name:    "time attr",
			attr:    slog.Time("key", time.Now()),
			wantLen: 1,
		},
		{
			name:    "duration attr",
			attr:    slog.Duration("key", time.Second),
			wantLen: 1,
		},
		{
			name:    "any attr",
			attr:    slog.Any("key", map[string]int{"a": 1}),
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := convertSlogAttr(tt.attr)
			assert.Len(t, fields, tt.wantLen, "convertSlogAttr should return correct number of fields")
			if tt.wantLen > 0 {
				assert.NotEmpty(t, fields[0].Key, "field should have key")
			}
		})
	}
}

func TestZapHandler_Integration(t *testing.T) {
	t.Run("end-to-end logging with all levels", func(t *testing.T) {
		logger := zaptest.NewLogger(t, zaptest.Level(zapcore.DebugLevel))
		handler := NewZapHandler(logger)
		slogger := slog.New(handler)

		// Test all log levels
		slogger.Debug("debug message", "key", "value")
		slogger.Info("info message", "count", 42)
		slogger.Warn("warn message", "enabled", true)
		slogger.Error("error message")

		// Test WithAttrs
		childLogger := slogger.With("attr1", "value1").With("attr2", "value2")
		childLogger.Info("message with attrs")

		// Test WithGroup
		groupLogger := slogger.WithGroup("testGroup")
		groupLogger.Info("message in group")
	})
}
