package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"polynux/disgoroq/triggerwords"

	"gopkg.in/yaml.v3"
)

// envVarRegex matches ${VAR_NAME} or ${VAR_NAME:-default} patterns
var envVarRegex = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::-([^}]*))?\}`)

// Load reads configuration from a YAML file at the specified path.
// Environment variables can be interpolated using ${VAR_NAME} or ${VAR_NAME:-default} syntax.
// Returns an error if the file doesn't exist or if required environment variables are missing.
func Load(path string) (*Config, error) {
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found: %s", path)
	}

	// Read the file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Interpolate environment variables
	interpolated, err := interpolateEnvVars(data)
	if err != nil {
		return nil, fmt.Errorf("failed to interpolate environment variables: %w", err)
	}

	// Start with defaults
	config := DefaultConfig()

	// Parse YAML into config
	if err := yaml.Unmarshal(interpolated, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	config.Bot.TriggerWords = triggerwords.NormalizeAll(config.Bot.TriggerWords)

	// Convert durations from parsed values (YAML stores them as ms/seconds)
	convertDurations(config)

	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return config, nil
}

// interpolateEnvVars replaces ${VAR_NAME} and ${VAR_NAME:-default} patterns with environment variable values.
// Returns an error if a required variable (no default) is not set.
func interpolateEnvVars(data []byte) ([]byte, error) {
	var errs []string

	result := envVarRegex.ReplaceAllFunc(data, func(match []byte) []byte {
		// Extract variable name and default value
		submatches := envVarRegex.FindSubmatch(match)
		if len(submatches) < 2 {
			return match // Should not happen if regex is correct
		}

		varName := string(submatches[1])
		envValue := os.Getenv(varName)

		// Check if there's a default value (submatches[2])
		hasDefault := len(submatches) > 2 && submatches[2] != nil
		defaultValue := ""
		if hasDefault {
			defaultValue = string(submatches[2])
		}

		// If env var is set, use it
		if envValue != "" {
			return []byte(envValue)
		}

		// If env var is not set and has default, use default
		if hasDefault {
			return []byte(defaultValue)
		}

		// Required variable is missing
		errs = append(errs, fmt.Sprintf("required environment variable %s is not set", varName))
		return match
	})

	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", errs[0])
	}

	return result, nil
}

// convertDurations handles conversion of duration fields from their YAML representations.
// RetryConfig stores durations as milliseconds in YAML.
// MemoryConfig stores SummaryInterval as seconds in YAML.
func convertDurations(config *Config) {
	// Convert RetryConfig durations from milliseconds
	if config.AI.Retry.InitialDelayMs > 0 {
		config.AI.Retry.InitialDelay = time.Duration(config.AI.Retry.InitialDelayMs) * time.Millisecond
	} else {
		// Use default
		config.AI.Retry.InitialDelay = 500 * time.Millisecond
	}

	if config.AI.Retry.MaxDelayMs > 0 {
		config.AI.Retry.MaxDelay = time.Duration(config.AI.Retry.MaxDelayMs) * time.Millisecond
	} else {
		// Use default
		config.AI.Retry.MaxDelay = 5 * time.Second
	}

	// Convert MemoryConfig SummaryInterval from seconds
	if config.Memory.SummaryIntervalSeconds > 0 {
		config.Memory.SummaryInterval = time.Duration(config.Memory.SummaryIntervalSeconds) * time.Second
	} else {
		// Use default
		config.Memory.SummaryInterval = 1 * time.Hour
	}
}

// MustLoad is like Load but panics on error.
// Use this for applications that should fail immediately on config errors.
func MustLoad(path string) *Config {
	config, err := Load(path)
	if err != nil {
		panic(fmt.Sprintf("failed to load configuration: %v", err))
	}
	return config
}

// Save writes the configuration to a YAML file at the specified path.
// This is primarily useful for generating example config files.
func Save(config *Config, path string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// parseBool parses a string as a boolean, accepting common boolean representations.
func parseBool(value string, defaultValue bool) bool {
	switch value {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}

// parseInt parses a string as an integer, returning the default on error.
func parseInt(value string, defaultValue int) int {
	if value == "" {
		return defaultValue
	}
	i, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return i
}

// parseFloat parses a string as a float, returning the default on error.
func parseFloat(value string, defaultValue float64) float64 {
	if value == "" {
		return defaultValue
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return defaultValue
	}
	return f
}
