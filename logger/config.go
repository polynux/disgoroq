package logger

import (
	"strings"

	configpkg "polynux/disgoroq/config"
)

// Config holds logging configuration (kept for backward compatibility)
type Config struct {
	Enabled             bool
	LogToDB             bool
	EventLoggingEnabled bool
	Level               string
	Encoding            string
	RetentionDays       int
	DBLogLevel          DBLogLevel
}

var logConfig *Config

// InitFromConfig initializes the logger configuration from the central config package.
// This should be called during application startup.
func InitFromConfig(cfg *configpkg.LoggingConfig) {
	logConfig = &Config{
		Enabled:             cfg.Enabled,
		LogToDB:             cfg.LogToDB,
		EventLoggingEnabled: cfg.EventLoggingEnabled,
		Level:               cfg.Level,
		Encoding:            cfg.Encoding,
		RetentionDays:       cfg.RetentionDays,
		DBLogLevel:          ParseDBLogLevel(cfg.DBLogLevel),
	}
}

// GetConfig returns the current logging configuration.
// Returns a default configuration if InitFromConfig has not been called.
func GetConfig() *Config {
	if logConfig == nil {
		// Return default config if not initialized
		defaults := configpkg.GetLoggingConfigDefaults()
		return &Config{
			Enabled:             defaults.Enabled,
			LogToDB:             defaults.LogToDB,
			EventLoggingEnabled: defaults.EventLoggingEnabled,
			Level:               defaults.Level,
			Encoding:            defaults.Encoding,
			RetentionDays:       defaults.RetentionDays,
			DBLogLevel:          ParseDBLogLevel(defaults.DBLogLevel),
		}
	}
	return logConfig
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