package logger

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	cfg "polynux/disgoroq/config"
	"polynux/disgoroq/database"
)

func TestWrapperFunctions_Disabled(t *testing.T) {
	// Initialize with disabled logging
	logConfig = nil
	InitFromConfig(&cfg.LoggingConfig{
		Enabled:             false,
		LogToDB:             false,
		EventLoggingEnabled: false,
		Level:               "info",
		Encoding:            "json",
		RetentionDays:       7,
		DBLogLevel:          "info",
	})

	var buf bytes.Buffer
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		MessageKey: "msg",
	})
	core := zapcore.NewCore(encoder, zapcore.AddSync(&buf), zapcore.DebugLevel)
	testLogger := zap.New(core)

	originalLog := Log
	Log = testLogger
	defer func() { Log = originalLog }()

	Debug("debug message")
	Info("info message")
	Warn("warn message")
	Error("error message")

	output := buf.String()
	assert.Empty(t, output, "No logs should be written when logging is disabled")
}

func TestWrapperFunctions_Enabled(t *testing.T) {
	// Initialize with enabled logging
	logConfig = nil
	InitFromConfig(&cfg.LoggingConfig{
		Enabled:             true,
		LogToDB:             false,
		EventLoggingEnabled: true,
		Level:               "debug",
		Encoding:            "json",
		RetentionDays:       7,
		DBLogLevel:          "info",
	})

	var buf bytes.Buffer
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		MessageKey: "msg",
		LevelKey:   "level",
	})
	core := zapcore.NewCore(encoder, zapcore.AddSync(&buf), zapcore.DebugLevel)
	testLogger := zap.New(core)

	originalLog := Log
	Log = testLogger
	defer func() { Log = originalLog }()

	Info("test info message")
	Warn("test warn message")
	Error("test error message")

	output := buf.String()
	assert.Contains(t, output, "test info message")
	assert.Contains(t, output, "test warn message")
	assert.Contains(t, output, "test error message")
}

func TestLogEvent_Disabled(t *testing.T) {
	// Initialize with event logging disabled
	logConfig = nil
	InitFromConfig(&cfg.LoggingConfig{
		Enabled:             true,
		LogToDB:             false,
		EventLoggingEnabled: false,
		Level:               "info",
		Encoding:            "json",
		RetentionDays:       7,
		DBLogLevel:          "info",
	})

	ctx := context.Background()
	event := &database.BotEvent{
		EventType: database.EventMessageReceived,
		GuildID:   "123",
	}

	LogEvent(ctx, event)
}

func TestLogEvent_Enabled_NoRepo(t *testing.T) {
	// Initialize with event logging enabled
	logConfig = nil
	InitFromConfig(&cfg.LoggingConfig{
		Enabled:             true,
		LogToDB:             false,
		EventLoggingEnabled: true,
		Level:               "info",
		Encoding:            "json",
		RetentionDays:       7,
		DBLogLevel:          "info",
	})

	SetEventRepository(nil)

	ctx := context.Background()
	event := &database.BotEvent{
		EventType: database.EventMessageReceived,
		GuildID:   "123",
	}

	LogEvent(ctx, event)
}

func TestLogMessageEvent(t *testing.T) {
	// Initialize with event logging enabled
	logConfig = nil
	InitFromConfig(&cfg.LoggingConfig{
		Enabled:             true,
		LogToDB:             false,
		EventLoggingEnabled: true,
		Level:               "info",
		Encoding:            "json",
		RetentionDays:       7,
		DBLogLevel:          "info",
	})

	ctx := context.Background()
	details := &database.EventDetails{
		MessagesCount: 5,
		ImageCount:    2,
	}

	LogMessageEvent(ctx, database.EventContextBuilt, "guild123", "channel456", "msg789", "user999", details)
}