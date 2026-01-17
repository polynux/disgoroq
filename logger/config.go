package logger

import (
	"log"
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
}

var config *Config

func init() {
	err := godotenv.Load(".env.local")
	if err != nil {
		log.Fatal("Error loading .env file")
	}

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
