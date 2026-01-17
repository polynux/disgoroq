package logger

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBoolEnv(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{"true string", "true", false, true},
		{"TRUE uppercase", "TRUE", false, true},
		{"1 string", "1", false, true},
		{"yes string", "yes", false, true},
		{"on string", "on", false, true},
		{"enabled string", "enabled", false, true},
		{"false string", "false", true, false},
		{"FALSE uppercase", "FALSE", true, false},
		{"0 string", "0", true, false},
		{"no string", "no", true, false},
		{"off string", "off", true, false},
		{"disabled string", "disabled", true, false},
		{"empty string uses default", "", true, true},
		{"invalid string uses default", "invalid", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_BOOL_ENV"
			os.Setenv(key, tt.envValue)
			defer os.Unsetenv(key)

			result := getBoolEnv(key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetIntEnv(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		defaultValue int
		expected     int
	}{
		{"valid integer", "42", 0, 42},
		{"zero value", "0", 10, 0},
		{"negative value", "-5", 0, -5},
		{"empty string uses default", "", 99, 99},
		{"invalid string uses default", "invalid", 88, 88},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_INT_ENV"
			os.Setenv(key, tt.envValue)
			defer os.Unsetenv(key)

			result := getIntEnv(key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadConfig(t *testing.T) {
	originalLogEnabled := os.Getenv("LOG_ENABLED")
	originalLogToDB := os.Getenv("LOG_TO_DB")
	originalEventLogging := os.Getenv("EVENT_LOGGING_ENABLED")
	originalLogLevel := os.Getenv("LOG_LEVEL")
	originalLogEncoding := os.Getenv("LOG_ENCODING")
	originalRetentionDays := os.Getenv("EVENT_RETENTION_DAYS")

	defer func() {
		os.Setenv("LOG_ENABLED", originalLogEnabled)
		os.Setenv("LOG_TO_DB", originalLogToDB)
		os.Setenv("EVENT_LOGGING_ENABLED", originalEventLogging)
		os.Setenv("LOG_LEVEL", originalLogLevel)
		os.Setenv("LOG_ENCODING", originalLogEncoding)
		os.Setenv("EVENT_RETENTION_DAYS", originalRetentionDays)
		config = nil
	}()

	os.Setenv("LOG_ENABLED", "false")
	os.Setenv("LOG_TO_DB", "true")
	os.Setenv("EVENT_LOGGING_ENABLED", "false")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("LOG_ENCODING", "console")
	os.Setenv("EVENT_RETENTION_DAYS", "14")

	cfg := loadConfig()

	assert.False(t, cfg.Enabled)
	assert.True(t, cfg.LogToDB)
	assert.False(t, cfg.EventLoggingEnabled)
	assert.Equal(t, "debug", cfg.Level)
	assert.Equal(t, "console", cfg.Encoding)
	assert.Equal(t, 14, cfg.RetentionDays)
}

func TestLoadConfig_Defaults(t *testing.T) {
	originalLogEnabled := os.Getenv("LOG_ENABLED")
	originalLogToDB := os.Getenv("LOG_TO_DB")
	originalEventLogging := os.Getenv("EVENT_LOGGING_ENABLED")
	originalLogLevel := os.Getenv("LOG_LEVEL")
	originalLogEncoding := os.Getenv("LOG_ENCODING")
	originalRetentionDays := os.Getenv("EVENT_RETENTION_DAYS")

	defer func() {
		os.Setenv("LOG_ENABLED", originalLogEnabled)
		os.Setenv("LOG_TO_DB", originalLogToDB)
		os.Setenv("EVENT_LOGGING_ENABLED", originalEventLogging)
		os.Setenv("LOG_LEVEL", originalLogLevel)
		os.Setenv("LOG_ENCODING", originalLogEncoding)
		os.Setenv("EVENT_RETENTION_DAYS", originalRetentionDays)
		config = nil
	}()

	os.Unsetenv("LOG_ENABLED")
	os.Unsetenv("LOG_TO_DB")
	os.Unsetenv("EVENT_LOGGING_ENABLED")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("LOG_ENCODING")
	os.Unsetenv("EVENT_RETENTION_DAYS")

	cfg := loadConfig()

	assert.True(t, cfg.Enabled)
	assert.False(t, cfg.LogToDB)
	assert.True(t, cfg.EventLoggingEnabled)
	assert.Equal(t, "info", cfg.Level)
	assert.Equal(t, "json", cfg.Encoding)
	assert.Equal(t, 7, cfg.RetentionDays)
}
