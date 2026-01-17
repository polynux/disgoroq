package logger

import (
	"strings"

	"polynux/disgoroq/database"
)

type DBLogLevel int

const (
	DBLogLevelNone DBLogLevel = iota
	DBLogLevelError
	DBLogLevelWarn
	DBLogLevelInfo
	DBLogLevelDebug
	DBLogLevelAll
)

var dbLogLevelMapping = map[database.EventType]DBLogLevel{
	database.EventAICallFailed:     DBLogLevelError,
	database.EventContextFailed:    DBLogLevelError,
	database.EventResponseFailed:   DBLogLevelError,
	database.EventRateLimited:      DBLogLevelWarn,
	database.EventEmptyResponse:    DBLogLevelWarn,
	database.EventThresholdSkipped: DBLogLevelWarn,
	database.EventMessageReceived:  DBLogLevelInfo,
	database.EventContextBuilt:     DBLogLevelInfo,
	database.EventResponseSent:     DBLogLevelInfo,
	database.EventAICallStart:      DBLogLevelDebug,
	database.EventAICallSuccess:    DBLogLevelDebug,
	database.EventStateOff:         DBLogLevelDebug,
}

func ParseDBLogLevel(level string) DBLogLevel {
	switch strings.ToLower(level) {
	case "none":
		return DBLogLevelNone
	case "error":
		return DBLogLevelError
	case "warn", "warning":
		return DBLogLevelWarn
	case "info":
		return DBLogLevelInfo
	case "debug":
		return DBLogLevelDebug
	case "all":
		return DBLogLevelAll
	default:
		return DBLogLevelInfo
	}
}

func ShouldLogToDB(eventType database.EventType, currentLevel DBLogLevel) bool {
	if currentLevel == DBLogLevelNone {
		return false
	}

	if currentLevel == DBLogLevelAll {
		return true
	}

	requiredLevel, exists := dbLogLevelMapping[eventType]
	if !exists {
		requiredLevel = DBLogLevelInfo
	}

	return currentLevel >= requiredLevel
}

func GetDBLogLevelName(level DBLogLevel) string {
	switch level {
	case DBLogLevelNone:
		return "none"
	case DBLogLevelError:
		return "error"
	case DBLogLevelWarn:
		return "warn"
	case DBLogLevelInfo:
		return "info"
	case DBLogLevelDebug:
		return "debug"
	case DBLogLevelAll:
		return "all"
	default:
		return "unknown"
	}
}

func GetAllDBLogLevels() []string {
	return []string{"none", "error", "warn", "info", "debug", "all"}
}
