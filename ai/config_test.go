package ai

import (
	"fmt"
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
