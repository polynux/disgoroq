// Package config provides centralized configuration management for DisgoroQ.
//
// Configuration is loaded from a YAML file (default: config.yaml) with support for
// environment variable interpolation. Secrets should be provided via environment
// variables using the ${VAR_NAME} or ${VAR_NAME:-default} syntax.
//
// Example YAML configuration:
//
//	discord:
//	  token: "${DISCORD_TOKEN}"
//
//	database:
//	  url: "${DB_URL}"
//	  token: "${DB_TOKEN}"
//	  local: false
//
//	ai:
//	  groq:
//	    api_key: "${GROQ_API_KEY}"
//	    model: "openai/gpt-oss-20b"
//	    vision_model: "meta-llama/llama-4-scout-17b-16e-instruct"
//
// The package provides type-safe configuration structs with validation at startup
// to catch configuration errors early.
package config

import (
	"fmt"
	"os"
	"time"
)

// DefaultConfigPath is the default configuration file path.
const DefaultConfigPath = "config.yaml"

// LoadDefault loads configuration from the default path (config.yaml).
// Returns an error if the file doesn't exist or if required fields are missing.
func LoadDefault() (*Config, error) {
	return Load(DefaultConfigPath)
}

// MustLoadDefault loads configuration from the default path and panics on error.
// Use this for applications that should fail immediately on config errors.
func MustLoadDefault() *Config {
	return MustLoad(DefaultConfigPath)
}

// GetRetryConfigDefaults returns the default retry configuration.
// This is useful for components that need retry logic but don't need full config.
func GetRetryConfigDefaults() RetryConfig {
	return RetryConfig{
		MaxRetries:    2,
		InitialDelay:  500 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}
}

// GetMemoryConfigDefaults returns the default memory configuration.
// This is useful for components that need memory settings but don't need full config.
func GetMemoryConfigDefaults() MemoryConfig {
	return MemoryConfig{
		Enabled:            true,
		OllamaURL:          "http://localhost:11434",
		EmbeddingModel:     "nomic-embed-text",
		SummaryModel:       "llama3-8b-8192",
		BufferThreshold:    10,
		SummaryInterval:    1 * time.Hour,
		MaxContextMessages: 5,
		MaxSummaryContext:  3,
	}
}

// GetLoggingConfigDefaults returns the default logging configuration.
func GetLoggingConfigDefaults() LoggingConfig {
	return LoggingConfig{
		Enabled:             true,
		LogToDB:             false,
		EventLoggingEnabled: true,
		Level:               "info",
		Encoding:            "json",
		RetentionDays:       7,
		DBLogLevel:          "info",
	}
}

// GetEmojiConfigDefaults returns the default emoji configuration.
func GetEmojiConfigDefaults() EmojiConfig {
	return EmojiConfig{
		CacheTTLMinutes: 60,
	}
}

// GetHoroscopeConfigDefaults returns the default horoscope configuration.
func GetHoroscopeConfigDefaults() HoroscopeConfig {
	return HoroscopeConfig{
		IncludeEmojis: true,
	}
}

// GetReengageConfigDefaults returns the default reengage configuration.
func GetReengageConfigDefaults() ReengageConfig {
	return ReengageConfig{
		CheckIntervalSeconds:     300,
		DefaultInactivityMinutes: 30,
		DefaultChance:            0.01,
	}
}

// Environment variable names for reference.
const (
	// Discord
	EnvDiscordToken = "DISCORD_TOKEN"

	// Database
	EnvDBURL   = "DB_URL"
	EnvDBToken = "DB_TOKEN"

	// AI - Groq
	EnvGroqAPIKey      = "GROQ_API_KEY"
	EnvGroqModel       = "GROQ_MODEL"
	EnvGroqVisionModel = "GROQ_VISION_MODEL"

	// AI - Ollama
	EnvOllamaEnabled     = "OLLAMA_ENABLED"
	EnvOllamaURL         = "OLLAMA_API_URL"
	EnvOllamaModel       = "OLLAMA_MODEL"
	EnvOllamaVisionModel = "OLLAMA_VISION_MODEL"

	// AI - Retry
	EnvAIMaxRetries          = "AI_MAX_RETRIES"
	EnvAIRetryInitialDelayMs = "AI_RETRY_INITIAL_DELAY_MS"
	EnvAIRetryMaxDelayMs     = "AI_RETRY_MAX_DELAY_MS"
	EnvAIRetryBackoff        = "AI_RETRY_BACKOFF"
	EnvAIRetryOnEmpty        = "AI_RETRY_ON_EMPTY"
	EnvAIRetryOnError        = "AI_RETRY_ON_ERROR"

	// AI - General
	EnvAIFallbackEnabled   = "AI_FALLBACK_ENABLED"
	EnvAIMinResponseLength = "AI_MIN_RESPONSE_LENGTH"

	// Logging
	EnvLogEnabled          = "LOG_ENABLED"
	EnvLogToDB             = "LOG_TO_DB"
	EnvLogLevel            = "LOG_LEVEL"
	EnvLogEncoding         = "LOG_ENCODING"
	EnvEventLoggingEnabled = "EVENT_LOGGING_ENABLED"
	EnvEventRetentionDays  = "EVENT_RETENTION_DAYS"
	EnvDBLogLevel          = "DB_LOG_LEVEL"

	// Memory
	EnvMemoryEnabled            = "MEMORY_ENABLED"
	EnvMemoryOllamaURL          = "MEMORY_OLLAMA_URL"
	EnvMemoryEmbeddingModel     = "MEMORY_EMBEDDING_MODEL"
	EnvMemorySummaryModel       = "MEMORY_SUMMARY_MODEL"
	EnvMemoryBufferThreshold    = "MEMORY_BUFFER_THRESHOLD"
	EnvMemorySummaryInterval    = "MEMORY_SUMMARY_INTERVAL"
	EnvMemoryMaxContextMessages = "MEMORY_MAX_CONTEXT_MESSAGES"
	EnvMemoryMaxSummaryContext  = "MEMORY_MAX_SUMMARY_CONTEXT"

	// Emoji
	EnvEmojiCacheTTLMinutes = "EMOJI_CACHE_TTL_MINUTES"

	// Horoscope
	EnvHoroscopeIncludeEmojis = "HOROSCOPE_INCLUDE_EMOJIS"

	// Reengage
	EnvReengageCheckIntervalSeconds     = "REENGAGE_CHECK_INTERVAL_SECONDS"
	EnvReengageDefaultInactivityMinutes = "REENGAGE_DEFAULT_INACTIVITY_MINUTES"
	EnvReengageDefaultChance            = "REENGAGE_DEFAULT_CHANCE"
)

// GetEnv returns the value of an environment variable or the default value if not set.
// This is a convenience function for manual environment variable access.
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetEnvBool returns the boolean value of an environment variable or the default if not set/invalid.
func GetEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	switch value {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}

// GetEnvInt returns the integer value of an environment variable or the default if not set/invalid.
func GetEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	var result int
	if _, err := fmt.Sscanf(value, "%d", &result); err != nil {
		return defaultValue
	}
	return result
}

// GetEnvFloat64 returns the float64 value of an environment variable or the default if not set/invalid.
func GetEnvFloat64(key string, defaultValue float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	var result float64
	if _, err := fmt.Sscanf(value, "%f", &result); err != nil {
		return defaultValue
	}
	return result
}
