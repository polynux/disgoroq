package logger

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Enabled             bool
	LogToDB             bool
	EventLoggingEnabled bool
	Level               string
	Encoding            string
	RetentionDays       int
	DBLogLevel          DBLogLevel
}

var config *Config

func init() {
	_ = godotenv.Load(".env.local")
	config = loadConfig()
}

func loadConfig() *Config {
	return &Config{
		Enabled:             getBoolEnv("LOG_ENABLED", true),
		LogToDB:             getBoolEnv("LOG_TO_DB", false),
		EventLoggingEnabled: getBoolEnv("EVENT_LOGGING_ENABLED", true),
		Level:               getEnv("LOG_LEVEL", "info"),
		Encoding:            getEnv("LOG_ENCODING", "json"),
		RetentionDays:       getIntEnv("EVENT_RETENTION_DAYS", 7),
		DBLogLevel:          ParseDBLogLevel(getEnv("DB_LOG_LEVEL", "info")),
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getBoolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	switch strings.ToLower(value) {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}

func getIntEnv(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}

func GetConfig() *Config {
	if config == nil {
		config = loadConfig()
	}
	return config
}

func IsEnabled() bool {
	return GetConfig().Enabled
}

func IsDBLoggingEnabled() bool {
	return GetConfig().LogToDB
}

func IsEventLoggingEnabled() bool {
	return GetConfig().EventLoggingEnabled
}

func GetDBLogLevel() DBLogLevel {
	return GetConfig().DBLogLevel
}

// IsDebugMode returns true if log level is set to debug
func IsDebugMode() bool {
	return strings.ToLower(GetConfig().Level) == "debug"
}
