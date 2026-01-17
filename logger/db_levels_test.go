package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"polynux/disgoroq/database"
)

func TestParseDBLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected DBLogLevel
	}{
		{"none lowercase", "none", DBLogLevelNone},
		{"NONE uppercase", "NONE", DBLogLevelNone},
		{"error lowercase", "error", DBLogLevelError},
		{"ERROR uppercase", "ERROR", DBLogLevelError},
		{"warn lowercase", "warn", DBLogLevelWarn},
		{"WARN uppercase", "WARN", DBLogLevelWarn},
		{"warning alternative", "warning", DBLogLevelWarn},
		{"info lowercase", "info", DBLogLevelInfo},
		{"INFO uppercase", "INFO", DBLogLevelInfo},
		{"debug lowercase", "debug", DBLogLevelDebug},
		{"DEBUG uppercase", "DEBUG", DBLogLevelDebug},
		{"all lowercase", "all", DBLogLevelAll},
		{"ALL uppercase", "ALL", DBLogLevelAll},
		{"invalid defaults to info", "invalid", DBLogLevelInfo},
		{"empty defaults to info", "", DBLogLevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseDBLogLevel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDBLogLevelName(t *testing.T) {
	tests := []struct {
		level    DBLogLevel
		expected string
	}{
		{DBLogLevelNone, "none"},
		{DBLogLevelError, "error"},
		{DBLogLevelWarn, "warn"},
		{DBLogLevelInfo, "info"},
		{DBLogLevelDebug, "debug"},
		{DBLogLevelAll, "all"},
		{DBLogLevel(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := GetDBLogLevelName(tt.level)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetAllDBLogLevels(t *testing.T) {
	levels := GetAllDBLogLevels()
	expected := []string{"none", "error", "warn", "info", "debug", "all"}
	assert.Equal(t, expected, levels)
}

func TestShouldLogToDB(t *testing.T) {
	tests := []struct {
		name         string
		eventType    database.EventType
		currentLevel DBLogLevel
		expected     bool
	}{
		// None level
		{"none level with error event", database.EventAICallFailed, DBLogLevelNone, false},
		{"none level with info event", database.EventMessageReceived, DBLogLevelNone, false},
		{"none level with debug event", database.EventAICallStart, DBLogLevelNone, false},

		// Error level
		{"error level with error event", database.EventAICallFailed, DBLogLevelError, true},
		{"error level with warn event", database.EventRateLimited, DBLogLevelError, false},
		{"error level with info event", database.EventMessageReceived, DBLogLevelError, false},
		{"error level with debug event", database.EventAICallStart, DBLogLevelError, false},

		// Warn level
		{"warn level with error event", database.EventAICallFailed, DBLogLevelWarn, true},
		{"warn level with warn event", database.EventRateLimited, DBLogLevelWarn, true},
		{"warn level with info event", database.EventMessageReceived, DBLogLevelWarn, false},
		{"warn level with debug event", database.EventAICallStart, DBLogLevelWarn, false},

		// Info level
		{"info level with error event", database.EventAICallFailed, DBLogLevelInfo, true},
		{"info level with warn event", database.EventRateLimited, DBLogLevelInfo, true},
		{"info level with info event", database.EventMessageReceived, DBLogLevelInfo, true},
		{"info level with debug event", database.EventAICallStart, DBLogLevelInfo, false},

		// Debug level
		{"debug level with error event", database.EventAICallFailed, DBLogLevelDebug, true},
		{"debug level with warn event", database.EventRateLimited, DBLogLevelDebug, true},
		{"debug level with info event", database.EventMessageReceived, DBLogLevelDebug, true},
		{"debug level with debug event", database.EventAICallStart, DBLogLevelDebug, true},

		// All level
		{"all level with error event", database.EventAICallFailed, DBLogLevelAll, true},
		{"all level with debug event", database.EventAICallStart, DBLogLevelAll, true},
		{"all level with unknown event", database.EventType("unknown_event"), DBLogLevelAll, true},

		// Unknown event types
		{"unknown event with info level", database.EventType("unknown_event"), DBLogLevelInfo, true},
		{"unknown event with debug level", database.EventType("unknown_event"), DBLogLevelDebug, true},
		{"unknown event with error level", database.EventType("unknown_event"), DBLogLevelError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldLogToDB(tt.eventType, tt.currentLevel)
			assert.Equal(t, tt.expected, result, "Event %s with level %s should return %v",
				tt.eventType, GetDBLogLevelName(tt.currentLevel), tt.expected)
		})
	}
}

func TestShouldLogToDB_EdgeCases(t *testing.T) {
	levels := []DBLogLevel{DBLogLevelError, DBLogLevelWarn, DBLogLevelInfo, DBLogLevelDebug}

	for i, level := range levels {
		assert.True(t, ShouldLogToDB(database.EventAICallFailed, level),
			"Error events should be logged at %s level", GetDBLogLevelName(level))

		if i >= 1 {
			assert.True(t, ShouldLogToDB(database.EventRateLimited, level),
				"Warn events should be logged at %s level", GetDBLogLevelName(level))
		} else {
			assert.False(t, ShouldLogToDB(database.EventRateLimited, level),
				"Warn events should NOT be logged at %s level", GetDBLogLevelName(level))
		}

		if i >= 2 {
			assert.True(t, ShouldLogToDB(database.EventMessageReceived, level),
				"Info events should be logged at %s level", GetDBLogLevelName(level))
		} else {
			assert.False(t, ShouldLogToDB(database.EventMessageReceived, level),
				"Info events should NOT be logged at %s level", GetDBLogLevelName(level))
		}

		if i >= 3 {
			assert.True(t, ShouldLogToDB(database.EventAICallStart, level),
				"Debug events should be logged at %s level", GetDBLogLevelName(level))
		} else {
			assert.False(t, ShouldLogToDB(database.EventAICallStart, level),
				"Debug events should NOT be logged at %s level", GetDBLogLevelName(level))
		}
	}
}
