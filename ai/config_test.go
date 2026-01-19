package ai

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	assert.Equal(t, 2, config.MaxRetries)
	assert.Equal(t, 500*time.Millisecond, config.InitialDelay)
	assert.Equal(t, 5*time.Second, config.MaxDelay)
	assert.Equal(t, 2.0, config.BackoffFactor)
	assert.True(t, config.RetryOnEmpty)
	assert.True(t, config.RetryOnError)
}

func TestLoadRetryConfigFromEnv(t *testing.T) {
	originalMaxRetries := os.Getenv("AI_MAX_RETRIES")
	originalInitialDelay := os.Getenv("AI_RETRY_INITIAL_DELAY_MS")
	originalMaxDelay := os.Getenv("AI_RETRY_MAX_DELAY_MS")
	originalBackoff := os.Getenv("AI_RETRY_BACKOFF")
	originalRetryOnEmpty := os.Getenv("AI_RETRY_ON_EMPTY")
	originalRetryOnError := os.Getenv("AI_RETRY_ON_ERROR")

	defer func() {
		os.Setenv("AI_MAX_RETRIES", originalMaxRetries)
		os.Setenv("AI_RETRY_INITIAL_DELAY_MS", originalInitialDelay)
		os.Setenv("AI_RETRY_MAX_DELAY_MS", originalMaxDelay)
		os.Setenv("AI_RETRY_BACKOFF", originalBackoff)
		os.Setenv("AI_RETRY_ON_EMPTY", originalRetryOnEmpty)
		os.Setenv("AI_RETRY_ON_ERROR", originalRetryOnError)
	}()

	tests := []struct {
		name     string
		envVars  map[string]string
		expected RetryConfig
	}{
		{
			name:    "default values when no env vars set",
			envVars: map[string]string{},
			expected: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
				RetryOnEmpty:  true,
				RetryOnError:  true,
			},
		},
		{
			name: "custom max retries",
			envVars: map[string]string{
				"AI_MAX_RETRIES": "5",
			},
			expected: RetryConfig{
				MaxRetries:    5,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
				RetryOnEmpty:  true,
				RetryOnError:  true,
			},
		},
		{
			name: "custom delays",
			envVars: map[string]string{
				"AI_RETRY_INITIAL_DELAY_MS": "1000",
				"AI_RETRY_MAX_DELAY_MS":     "10000",
			},
			expected: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  1000 * time.Millisecond,
				MaxDelay:      10 * time.Second,
				BackoffFactor: 2.0,
				RetryOnEmpty:  true,
				RetryOnError:  true,
			},
		},
		{
			name: "custom backoff factor",
			envVars: map[string]string{
				"AI_RETRY_BACKOFF": "3.0",
			},
			expected: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 3.0,
				RetryOnEmpty:  true,
				RetryOnError:  true,
			},
		},
		{
			name: "disable retry on empty",
			envVars: map[string]string{
				"AI_RETRY_ON_EMPTY": "false",
			},
			expected: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
				RetryOnEmpty:  false,
				RetryOnError:  true,
			},
		},
		{
			name: "disable retry on error",
			envVars: map[string]string{
				"AI_RETRY_ON_ERROR": "false",
			},
			expected: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
				RetryOnEmpty:  true,
				RetryOnError:  false,
			},
		},
		{
			name: "all custom values",
			envVars: map[string]string{
				"AI_MAX_RETRIES":            "3",
				"AI_RETRY_INITIAL_DELAY_MS": "750",
				"AI_RETRY_MAX_DELAY_MS":     "8000",
				"AI_RETRY_BACKOFF":          "1.5",
				"AI_RETRY_ON_EMPTY":         "false",
				"AI_RETRY_ON_ERROR":         "false",
			},
			expected: RetryConfig{
				MaxRetries:    3,
				InitialDelay:  750 * time.Millisecond,
				MaxDelay:      8 * time.Second,
				BackoffFactor: 1.5,
				RetryOnEmpty:  false,
				RetryOnError:  false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars first
			os.Unsetenv("AI_MAX_RETRIES")
			os.Unsetenv("AI_RETRY_INITIAL_DELAY_MS")
			os.Unsetenv("AI_RETRY_MAX_DELAY_MS")
			os.Unsetenv("AI_RETRY_BACKOFF")
			os.Unsetenv("AI_RETRY_ON_EMPTY")
			os.Unsetenv("AI_RETRY_ON_ERROR")

			// Set test env vars
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			config := LoadRetryConfigFromEnv()
			assert.Equal(t, tt.expected.MaxRetries, config.MaxRetries)
			assert.Equal(t, tt.expected.InitialDelay, config.InitialDelay)
			assert.Equal(t, tt.expected.MaxDelay, config.MaxDelay)
			assert.Equal(t, tt.expected.BackoffFactor, config.BackoffFactor)
			assert.Equal(t, tt.expected.RetryOnEmpty, config.RetryOnEmpty)
			assert.Equal(t, tt.expected.RetryOnError, config.RetryOnError)
		})
	}
}

func TestParseBoolEnv(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		defaultValue bool
		expected     bool
	}{
		{"true lowercase", "true", false, true},
		{"TRUE uppercase", "TRUE", false, true},
		{"True mixed case", "True", false, true},
		{"1 numeric", "1", false, true},
		{"yes", "yes", false, true},
		{"on", "on", false, true},
		{"enabled", "enabled", false, true},
		{"false lowercase", "false", true, false},
		{"FALSE uppercase", "FALSE", true, false},
		{"0 numeric", "0", true, false},
		{"no", "no", true, false},
		{"off", "off", true, false},
		{"disabled", "disabled", true, false},
		{"invalid value", "invalid", true, true},
		{"empty string", "", true, true},
		{"whitespace", "  true  ", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseBoolEnv(tt.value, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRetryConfigValidate(t *testing.T) {
	tests := []struct {
		name      string
		config    RetryConfig
		shouldErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
			},
			shouldErr: false,
		},
		{
			name: "negative max retries",
			config: RetryConfig{
				MaxRetries:    -1,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
			},
			shouldErr: true,
			errMsg:    "max retries cannot be negative",
		},
		{
			name: "zero initial delay",
			config: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  0,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
			},
			shouldErr: true,
			errMsg:    "initial delay must be positive",
		},
		{
			name: "negative max delay",
			config: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      -1 * time.Second,
				BackoffFactor: 2.0,
			},
			shouldErr: true,
			errMsg:    "max delay must be positive",
		},
		{
			name: "backoff factor too low",
			config: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  500 * time.Millisecond,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 0.5,
			},
			shouldErr: true,
			errMsg:    "backoff factor must be >= 1.0",
		},
		{
			name: "initial delay greater than max delay",
			config: RetryConfig{
				MaxRetries:    2,
				InitialDelay:  10 * time.Second,
				MaxDelay:      5 * time.Second,
				BackoffFactor: 2.0,
			},
			shouldErr: true,
			errMsg:    "initial delay (10s) cannot be greater than max delay (5s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.shouldErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetDelayForAttempt(t *testing.T) {
	config := RetryConfig{
		MaxRetries:    3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      1000 * time.Millisecond,
		BackoffFactor: 2.0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{-1, 0},
		{0, 0},
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, 1000 * time.Millisecond},
		{6, 1000 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			delay := config.GetDelayForAttempt(tt.attempt)
			assert.Equal(t, tt.expected, delay)
		})
	}
}

func TestPow(t *testing.T) {
	tests := []struct {
		base     float64
		exp      float64
		expected float64
	}{
		{2.0, 0, 1},
		{2.0, 1, 2},
		{2.0, 2, 4},
		{2.0, 3, 8},
		{1.5, 2, 2.25},
		{3.0, 3, 27},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%.1f^%.0f", tt.base, tt.exp), func(t *testing.T) {
			result := pow(tt.base, tt.exp)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRetryConfigString(t *testing.T) {
	config := RetryConfig{
		MaxRetries:    3,
		InitialDelay:  250 * time.Millisecond,
		MaxDelay:      3 * time.Second,
		BackoffFactor: 1.5,
		RetryOnEmpty:  false,
		RetryOnError:  true,
	}

	result := config.String()
	expected := "RetryConfig{MaxRetries:3, InitialDelay:250ms, MaxDelay:3s, BackoffFactor:1.5, RetryOnEmpty:false, RetryOnError:true}"
	assert.Equal(t, expected, result)
}

func TestGetDefaultRetryConfig(t *testing.T) {
	config := GetDefaultRetryConfig()
	assert.Equal(t, DefaultRetryConfig(), config)
}

func TestLoadRetryConfigFromEnvEdgeCases(t *testing.T) {
	originalMaxRetries := os.Getenv("AI_MAX_RETRIES")
	originalInitialDelay := os.Getenv("AI_RETRY_INITIAL_DELAY_MS")
	originalMaxDelay := os.Getenv("AI_RETRY_MAX_DELAY_MS")
	originalBackoff := os.Getenv("AI_RETRY_BACKOFF")

	defer func() {
		os.Setenv("AI_MAX_RETRIES", originalMaxRetries)
		os.Setenv("AI_RETRY_INITIAL_DELAY_MS", originalInitialDelay)
		os.Setenv("AI_RETRY_MAX_DELAY_MS", originalMaxDelay)
		os.Setenv("AI_RETRY_BACKOFF", originalBackoff)
	}()

	tests := []struct {
		name     string
		envVars  map[string]string
		validate func(t *testing.T, config RetryConfig)
	}{
		{
			name: "invalid max retries uses default",
			envVars: map[string]string{
				"AI_MAX_RETRIES": "invalid",
			},
			validate: func(t *testing.T, config RetryConfig) {
				assert.Equal(t, 2, config.MaxRetries)
			},
		},
		{
			name: "negative max retries uses default",
			envVars: map[string]string{
				"AI_MAX_RETRIES": "-5",
			},
			validate: func(t *testing.T, config RetryConfig) {
				assert.Equal(t, 2, config.MaxRetries)
			},
		},
		{
			name: "zero max retries is valid",
			envVars: map[string]string{
				"AI_MAX_RETRIES": "0",
			},
			validate: func(t *testing.T, config RetryConfig) {
				assert.Equal(t, 0, config.MaxRetries)
			},
		},
		{
			name: "invalid delay values use default",
			envVars: map[string]string{
				"AI_RETRY_INITIAL_DELAY_MS": "invalid",
				"AI_RETRY_MAX_DELAY_MS":     "invalid",
			},
			validate: func(t *testing.T, config RetryConfig) {
				assert.Equal(t, 500*time.Millisecond, config.InitialDelay)
				assert.Equal(t, 5*time.Second, config.MaxDelay)
			},
		},
		{
			name: "negative delay values use default",
			envVars: map[string]string{
				"AI_RETRY_INITIAL_DELAY_MS": "-100",
				"AI_RETRY_MAX_DELAY_MS":     "-1000",
			},
			validate: func(t *testing.T, config RetryConfig) {
				assert.Equal(t, 500*time.Millisecond, config.InitialDelay)
				assert.Equal(t, 5*time.Second, config.MaxDelay)
			},
		},
		{
			name: "invalid backoff factor uses default",
			envVars: map[string]string{
				"AI_RETRY_BACKOFF": "invalid",
			},
			validate: func(t *testing.T, config RetryConfig) {
				assert.Equal(t, 2.0, config.BackoffFactor)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("AI_MAX_RETRIES")
			os.Unsetenv("AI_RETRY_INITIAL_DELAY_MS")
			os.Unsetenv("AI_RETRY_MAX_DELAY_MS")
			os.Unsetenv("AI_RETRY_BACKOFF")

			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			config := LoadRetryConfigFromEnv()
			tt.validate(t, config)
		})
	}
}
