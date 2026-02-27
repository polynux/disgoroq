package ai

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RetryConfig contains configuration for retry logic
type RetryConfig struct {
	MaxRetries    int           // Maximum number of retry attempts (default: 2)
	InitialDelay  time.Duration // Initial delay between retries (default: 500ms)
	MaxDelay      time.Duration // Maximum delay between retries (default: 5s)
	BackoffFactor float64       // Exponential backoff factor (default: 2.0)
	RetryOnEmpty  bool          // Retry on empty responses (default: true)
	RetryOnError  bool          // Retry on API errors (default: true)
}

// DefaultRetryConfig returns the default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    2,
		InitialDelay:  500 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}
}

// Validate checks if the retry configuration is valid
func (c *RetryConfig) Validate() error {
	if c.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative: %d", c.MaxRetries)
	}
	if c.InitialDelay <= 0 {
		return fmt.Errorf("initial delay must be positive: %v", c.InitialDelay)
	}
	if c.MaxDelay <= 0 {
		return fmt.Errorf("max delay must be positive: %v", c.MaxDelay)
	}
	if c.BackoffFactor < 1.0 {
		return fmt.Errorf("backoff factor must be >= 1.0: %f", c.BackoffFactor)
	}
	if c.InitialDelay > c.MaxDelay {
		return fmt.Errorf("initial delay (%v) cannot be greater than max delay (%v)", c.InitialDelay, c.MaxDelay)
	}
	return nil
}

// GetDelayForAttempt calculates the delay for a specific retry attempt
func (c *RetryConfig) GetDelayForAttempt(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// Calculate exponential backoff delay
	delay := float64(c.InitialDelay) * pow(c.BackoffFactor, float64(attempt-1))

	// Cap at maximum delay
	if time.Duration(delay) > c.MaxDelay {
		return c.MaxDelay
	}

	return time.Duration(delay)
}

// pow calculates base^exp for float64
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// String returns a string representation of the retry configuration
func (c RetryConfig) String() string {
	return fmt.Sprintf("RetryConfig{MaxRetries:%d, InitialDelay:%v, MaxDelay:%v, BackoffFactor:%.1f, RetryOnEmpty:%t, RetryOnError:%t}",
		c.MaxRetries, c.InitialDelay, c.MaxDelay, c.BackoffFactor, c.RetryOnEmpty, c.RetryOnError)
}

// GetDefaultRetryConfig returns the default retry configuration as a convenience function
func GetDefaultRetryConfig() RetryConfig {
	return DefaultRetryConfig()
}

// getEnvWithDefault returns the environment variable value or default if not set
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getIntEnvWithDefault returns the environment variable as int or default if not set/invalid
func getIntEnvWithDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil && intVal >= 0 {
			return intVal
		}
	}
	return defaultValue
}

// parseBoolEnv parses a boolean environment variable value
func parseBoolEnv(value string, defaultValue bool) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "true", "1", "yes", "on", "enabled":
		return true
	case "false", "0", "no", "off", "disabled":
		return false
	default:
		return defaultValue
	}
}