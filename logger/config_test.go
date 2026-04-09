package logger

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	cfg "polynux/disgoroq/config"
)

func TestInitFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfgVal   cfg.LoggingConfig
		expected Config
	}{
		{
			name: "custom config",
			cfgVal: cfg.LoggingConfig{
				Enabled:             false,
				LogToDB:             true,
				EventLoggingEnabled: false,
				Level:               "debug",
				Encoding:            "console",
				RetentionDays:       14,
				DBLogLevel:          "debug",
			},
			expected: Config{
				Enabled:             false,
				LogToDB:             true,
				EventLoggingEnabled: false,
				Level:               "debug",
				Encoding:            "console",
				RetentionDays:       14,
				DBLogLevel:          DBLogLevelDebug,
			},
		},
		{
			name: "default config",
			cfgVal: cfg.LoggingConfig{
				Enabled:             true,
				LogToDB:             false,
				EventLoggingEnabled: true,
				Level:               "info",
				Encoding:            "json",
				RetentionDays:       7,
				DBLogLevel:          "info",
			},
			expected: Config{
				Enabled:             true,
				LogToDB:             false,
				EventLoggingEnabled: true,
				Level:               "info",
				Encoding:            "json",
				RetentionDays:       7,
				DBLogLevel:          DBLogLevelInfo,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global config
			logConfig = nil
			Log = nil

			InitFromConfig(&tt.cfgVal)
			result := GetConfig()

			assert.Equal(t, tt.expected.Enabled, result.Enabled)
			assert.Equal(t, tt.expected.LogToDB, result.LogToDB)
			assert.Equal(t, tt.expected.EventLoggingEnabled, result.EventLoggingEnabled)
			assert.Equal(t, tt.expected.Level, result.Level)
			assert.Equal(t, tt.expected.Encoding, result.Encoding)
			assert.Equal(t, tt.expected.RetentionDays, result.RetentionDays)
			assert.Equal(t, tt.expected.DBLogLevel, result.DBLogLevel)
			assert.NotNil(t, Log)
		})
	}
}

func TestGetConfig_WithoutInit(t *testing.T) {
	// Reset global config to nil
	logConfig = nil
	Log = nil

	// GetConfig should return defaults when not initialized
	result := GetConfig()

	// Should return default values
	assert.True(t, result.Enabled)
	assert.False(t, result.LogToDB)
	assert.True(t, result.EventLoggingEnabled)
	assert.Equal(t, "info", result.Level)
	assert.Equal(t, "json", result.Encoding)
	assert.Equal(t, 7, result.RetentionDays)
}

func TestAccessors(t *testing.T) {
	// Reset and initialize with custom config
	logConfig = nil
	Log = nil
	testCfg := &cfg.LoggingConfig{
		Enabled:             true,
		LogToDB:             true,
		EventLoggingEnabled: true,
		Level:               "debug",
		Encoding:            "console",
		RetentionDays:       14,
		DBLogLevel:          "all",
	}
	InitFromConfig(testCfg)

	assert.True(t, IsEnabled())
	assert.True(t, IsDBLoggingEnabled())
	assert.True(t, IsEventLoggingEnabled())
	assert.True(t, IsDebugMode())
	assert.Equal(t, DBLogLevelAll, GetDBLogLevel())
}

func TestIsDebugMode(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected bool
	}{
		{"debug lowercase", "debug", true},
		{"DEBUG uppercase", "DEBUG", true},
		{"Debug mixed case", "Debug", true},
		{"info level", "info", false},
		{"warn level", "warn", false},
		{"error level", "error", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logConfig = nil
			Log = nil
			testCfg := &cfg.LoggingConfig{
				Enabled: true,
				Level:   tt.level,
			}
			InitFromConfig(testCfg)

			result := IsDebugMode()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInitFromConfig_RebuildsLogger(t *testing.T) {
	logConfig = nil
	Log = zap.NewNop()

	InitFromConfig(&cfg.LoggingConfig{
		Enabled:       true,
		Level:         "debug",
		Encoding:      "console",
		RetentionDays: 3,
		DBLogLevel:    "warn",
	})

	assert.NotNil(t, Log)
	assert.Equal(t, 3, GetRetentionDays())
	assert.True(t, IsDebugMode())
}

func TestRebuildLogger_UsesHumanReadableTimestamp(t *testing.T) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(logTimeLayout)

	encoder := zapcore.NewJSONEncoder(encoderConfig)
	entryTime := time.Date(2026, time.April, 9, 8, 42, 13, 987000000, time.UTC)

	buf, err := encoder.EncodeEntry(zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Time:    entryTime,
		Message: "test log",
	}, nil)
	assert.NoError(t, err)
	assert.Contains(t, strings.TrimSpace(buf.String()), `"ts":"2026-04-09 08:42:13"`)
}
