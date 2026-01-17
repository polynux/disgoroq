package logger

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"polynux/disgoroq/database"
)

func TestWrapperFunctions_Disabled(t *testing.T) {
	os.Setenv("LOG_ENABLED", "false")
	defer os.Unsetenv("LOG_ENABLED")
	config = nil

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
	assert.Empty(t, output, "No logs should be written when LOG_ENABLED=false")
}

func TestWrapperFunctions_Enabled(t *testing.T) {
	os.Setenv("LOG_ENABLED", "true")
	defer os.Unsetenv("LOG_ENABLED")
	config = nil

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
	os.Setenv("EVENT_LOGGING_ENABLED", "false")
	defer os.Unsetenv("EVENT_LOGGING_ENABLED")
	config = nil

	ctx := context.Background()
	event := &database.BotEvent{
		EventType: database.EventMessageReceived,
		GuildID:   "123",
	}

	LogEvent(ctx, event)
}

func TestLogEvent_Enabled_NoRepo(t *testing.T) {
	os.Setenv("EVENT_LOGGING_ENABLED", "true")
	defer os.Unsetenv("EVENT_LOGGING_ENABLED")
	config = nil

	SetEventRepository(nil)

	ctx := context.Background()
	event := &database.BotEvent{
		EventType: database.EventMessageReceived,
		GuildID:   "123",
	}

	LogEvent(ctx, event)
}

func TestLogMessageEvent(t *testing.T) {
	os.Setenv("EVENT_LOGGING_ENABLED", "true")
	defer os.Unsetenv("EVENT_LOGGING_ENABLED")
	config = nil

	ctx := context.Background()
	details := &database.EventDetails{
		MessagesCount: 5,
		ImageCount:    2,
	}

	LogMessageEvent(ctx, database.EventContextBuilt, "guild123", "channel456", "msg789", "user999", details)
}
