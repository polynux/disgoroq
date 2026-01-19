package ai

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadServiceConfig(t *testing.T) {
	// Save original env vars
	originalGroqKey := os.Getenv("GROQ_API_KEY")
	originalOllamaEnabled := os.Getenv("OLLAMA_ENABLED")
	originalOllamaURL := os.Getenv("OLLAMA_API_URL")
	originalOllamaModel := os.Getenv("OLLAMA_MODEL")
	originalFallbackEnabled := os.Getenv("AI_FALLBACK_ENABLED")
	originalMinResponseLength := os.Getenv("AI_MIN_RESPONSE_LENGTH")
	originalMaxRetries := os.Getenv("AI_MAX_RETRIES")
	originalInitialDelay := os.Getenv("AI_RETRY_INITIAL_DELAY_MS")
	originalMaxDelay := os.Getenv("AI_RETRY_MAX_DELAY_MS")
	originalBackoff := os.Getenv("AI_RETRY_BACKOFF")
	originalRetryOnEmpty := os.Getenv("AI_RETRY_ON_EMPTY")
	originalRetryOnError := os.Getenv("AI_RETRY_ON_ERROR")

	defer func() {
		os.Setenv("GROQ_API_KEY", originalGroqKey)
		os.Setenv("OLLAMA_ENABLED", originalOllamaEnabled)
		os.Setenv("OLLAMA_API_URL", originalOllamaURL)
		os.Setenv("OLLAMA_MODEL", originalOllamaModel)
		os.Setenv("AI_FALLBACK_ENABLED", originalFallbackEnabled)
		os.Setenv("AI_MIN_RESPONSE_LENGTH", originalMinResponseLength)
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
		expected ServiceConfig
	}{
		{
			name:    "default configuration",
			envVars: map[string]string{},
			expected: ServiceConfig{
				GroqAPIKey:        "",
				OllamaEnabled:     false,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "dolphin3",
				RetryConfig:       DefaultRetryConfig(),
				MinResponseLength: 1,
				FallbackEnabled:   true,
			},
		},
		{
			name: "custom groq key",
			envVars: map[string]string{
				"GROQ_API_KEY": "test-key-123",
			},
			expected: ServiceConfig{
				GroqAPIKey:        "test-key-123",
				OllamaEnabled:     false,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "dolphin3",
				RetryConfig:       DefaultRetryConfig(),
				MinResponseLength: 1,
				FallbackEnabled:   true,
			},
		},
		{
			name: "enable ollama fallback",
			envVars: map[string]string{
				"GROQ_API_KEY":        "test-key-123",
				"OLLAMA_ENABLED":      "true",
				"OLLAMA_API_URL":      "http://ollama.local:11434",
				"OLLAMA_MODEL":        "llama2",
				"AI_FALLBACK_ENABLED": "true",
			},
			expected: ServiceConfig{
				GroqAPIKey:        "test-key-123",
				OllamaEnabled:     true,
				OllamaURL:         "http://ollama.local:11434",
				OllamaModel:       "llama2",
				RetryConfig:       DefaultRetryConfig(),
				MinResponseLength: 1,
				FallbackEnabled:   true,
			},
		},
		{
			name: "custom retry config",
			envVars: map[string]string{
				"GROQ_API_KEY":              "test-key-123",
				"AI_MAX_RETRIES":            "5",
				"AI_RETRY_INITIAL_DELAY_MS": "1000",
				"AI_RETRY_MAX_DELAY_MS":     "10000",
				"AI_RETRY_BACKOFF":          "3.0",
				"AI_RETRY_ON_EMPTY":         "false",
				"AI_RETRY_ON_ERROR":         "false",
			},
			expected: ServiceConfig{
				GroqAPIKey:    "test-key-123",
				OllamaEnabled: false,
				OllamaURL:     "http://localhost:11434",
				OllamaModel:   "dolphin3",
				RetryConfig: RetryConfig{
					MaxRetries:    5,
					InitialDelay:  1000 * time.Millisecond,
					MaxDelay:      10 * time.Second,
					BackoffFactor: 3.0,
					RetryOnEmpty:  false,
					RetryOnError:  false,
				},
				MinResponseLength: 1,
				FallbackEnabled:   true,
			},
		},
		{
			name: "custom min response length",
			envVars: map[string]string{
				"GROQ_API_KEY":           "test-key-123",
				"AI_MIN_RESPONSE_LENGTH": "10",
			},
			expected: ServiceConfig{
				GroqAPIKey:        "test-key-123",
				OllamaEnabled:     false,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "dolphin3",
				RetryConfig:       DefaultRetryConfig(),
				MinResponseLength: 10,
				FallbackEnabled:   true,
			},
		},
		{
			name: "disable fallback",
			envVars: map[string]string{
				"GROQ_API_KEY":        "test-key-123",
				"AI_FALLBACK_ENABLED": "false",
			},
			expected: ServiceConfig{
				GroqAPIKey:        "test-key-123",
				OllamaEnabled:     false,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "dolphin3",
				RetryConfig:       DefaultRetryConfig(),
				MinResponseLength: 1,
				FallbackEnabled:   false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars first
			os.Unsetenv("GROQ_API_KEY")
			os.Unsetenv("OLLAMA_ENABLED")
			os.Unsetenv("OLLAMA_API_URL")
			os.Unsetenv("OLLAMA_MODEL")
			os.Unsetenv("AI_FALLBACK_ENABLED")
			os.Unsetenv("AI_MIN_RESPONSE_LENGTH")
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

			config := LoadServiceConfig()

			assert.Equal(t, tt.expected.GroqAPIKey, config.GroqAPIKey)
			assert.Equal(t, tt.expected.OllamaEnabled, config.OllamaEnabled)
			assert.Equal(t, tt.expected.OllamaURL, config.OllamaURL)
			assert.Equal(t, tt.expected.OllamaModel, config.OllamaModel)
			assert.Equal(t, tt.expected.MinResponseLength, config.MinResponseLength)
			assert.Equal(t, tt.expected.FallbackEnabled, config.FallbackEnabled)

			// Check retry config
			assert.Equal(t, tt.expected.RetryConfig.MaxRetries, config.RetryConfig.MaxRetries)
			assert.Equal(t, tt.expected.RetryConfig.InitialDelay, config.RetryConfig.InitialDelay)
			assert.Equal(t, tt.expected.RetryConfig.MaxDelay, config.RetryConfig.MaxDelay)
			assert.Equal(t, tt.expected.RetryConfig.BackoffFactor, config.RetryConfig.BackoffFactor)
			assert.Equal(t, tt.expected.RetryConfig.RetryOnEmpty, config.RetryConfig.RetryOnEmpty)
			assert.Equal(t, tt.expected.RetryConfig.RetryOnError, config.RetryConfig.RetryOnError)
		})
	}
}

func TestServiceConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      ServiceConfig
		shouldError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: ServiceConfig{
				GroqAPIKey:        "test-key",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: false,
		},
		{
			name: "missing groq api key",
			config: ServiceConfig{
				GroqAPIKey:        "",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "GROQ_API_KEY is required",
		},
		{
			name: "invalid min response length",
			config: ServiceConfig{
				GroqAPIKey:        "test-key",
				MinResponseLength: 0,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "minimum response length must be at least 1",
		},
		{
			name: "invalid retry config",
			config: ServiceConfig{
				GroqAPIKey:        "test-key",
				MinResponseLength: 1,
				RetryConfig: RetryConfig{
					MaxRetries: -1, // Invalid
				},
			},
			shouldError: true,
			errorMsg:    "invalid retry config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.shouldError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewService(t *testing.T) {
	// This test requires actual provider creation, so we'll test with mock config
	config := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     false, // Disable Ollama to avoid connection issues
		OllamaURL:         "http://localhost:11434",
		OllamaModel:       "dolphin3",
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	service := NewService(config)

	assert.NotNil(t, service)
	assert.Equal(t, config, service.config)
	assert.NotNil(t, service.provider)

	// Should be a retry wrapper wrapping the groq provider
	retryWrapper, ok := service.provider.(*RetryWrapper)
	assert.True(t, ok, "Provider should be a RetryWrapper")
	assert.NotNil(t, retryWrapper)
}

func TestNewServiceNoGroqKey(t *testing.T) {
	config := ServiceConfig{
		GroqAPIKey:        "", // No API key
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	// Should not panic but return nil
	service := NewService(config)
	assert.Nil(t, service)
}

func TestServiceName(t *testing.T) {
	config := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	service := NewService(config)
	name := service.Name()

	// Should contain groq since that's the only provider
	assert.Contains(t, name, "groq")
}

func TestServiceAvailableModels(t *testing.T) {
	config := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	service := NewService(config)
	models := service.AvailableModels()

	// Should have models from Groq provider
	assert.NotEmpty(t, models)

	// Check that we have expected Groq models
	modelNames := make(map[string]bool)
	for _, model := range models {
		modelNames[model.Name] = true
	}

	// Should have some of the expected models
	expectedModels := []string{"openai/gpt-oss-20b", "llama-3-70b-versatile", "meta-llama/llama-4-scout-17b-16e-instruct"}
	hasExpected := false
	for _, expected := range expectedModels {
		if modelNames[expected] {
			hasExpected = true
			break
		}
	}
	assert.True(t, hasExpected, "Should have at least one expected model")
}

func TestServiceGetProviderInfo(t *testing.T) {
	config := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     true,
		OllamaURL:         "http://ollama.local:11434",
		OllamaModel:       "llama2",
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 5,
		FallbackEnabled:   true,
	}

	service := NewService(config)
	info := service.GetProviderInfo()

	assert.NotNil(t, info)
	assert.Equal(t, "groq", info["primary_provider"])
	assert.Equal(t, true, info["fallback_enabled"])
	assert.Equal(t, true, info["ollama_enabled"])
	assert.Equal(t, 5, info["min_response_length"])

	retryConfig, ok := info["retry_config"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, 2, retryConfig["max_retries"])
	assert.Equal(t, "500ms", retryConfig["initial_delay"])
	assert.Equal(t, "5s", retryConfig["max_delay"])
	assert.Equal(t, 2.0, retryConfig["backoff_factor"])
	assert.Equal(t, true, retryConfig["retry_on_empty"])
	assert.Equal(t, true, retryConfig["retry_on_error"])

	assert.Equal(t, "http://ollama.local:11434", info["ollama_url"])
	assert.Equal(t, "llama2", info["ollama_model"])
}

func TestServiceIsFallbackAvailable(t *testing.T) {
	// Test with fallback disabled
	config1 := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	service1 := NewService(config1)
	assert.False(t, service1.IsFallbackAvailable())

	// Test with fallback enabled but Ollama disabled
	config2 := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   true,
	}

	service2 := NewService(config2)
	assert.False(t, service2.IsFallbackAvailable())

	// Test with fallback enabled and Ollama enabled
	config3 := ServiceConfig{
		GroqAPIKey:        "test-key-123",
		OllamaEnabled:     true,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   true,
	}

	service3 := NewService(config3)
	if service3 != nil {
		t.Logf("Ollama fallback available: %v", service3.IsFallbackAvailable())
	} else {
		t.Log("Ollama provider not available in test environment")
	}
}

func TestGetEnvWithDefault(t *testing.T) {
	originalValue := os.Getenv("TEST_ENV_VAR")
	defer os.Setenv("TEST_ENV_VAR", originalValue)

	// Test with env var set
	os.Setenv("TEST_ENV_VAR", "custom-value")
	result := getEnvWithDefault("TEST_ENV_VAR", "default-value")
	assert.Equal(t, "custom-value", result)

	// Test with env var not set
	os.Unsetenv("TEST_ENV_VAR")
	result = getEnvWithDefault("TEST_ENV_VAR", "default-value")
	assert.Equal(t, "default-value", result)
}

func TestGetIntEnvWithDefault(t *testing.T) {
	originalValue := os.Getenv("TEST_INT_VAR")
	defer os.Setenv("TEST_INT_VAR", originalValue)

	// Test with valid int
	os.Setenv("TEST_INT_VAR", "42")
	result := getIntEnvWithDefault("TEST_INT_VAR", 10)
	assert.Equal(t, 42, result)

	// Test with invalid int
	os.Setenv("TEST_INT_VAR", "invalid")
	result = getIntEnvWithDefault("TEST_INT_VAR", 10)
	assert.Equal(t, 10, result)

	// Test with negative int (should use default)
	os.Setenv("TEST_INT_VAR", "-5")
	result = getIntEnvWithDefault("TEST_INT_VAR", 10)
	assert.Equal(t, 10, result)

	// Test with env var not set
	os.Unsetenv("TEST_INT_VAR")
	result = getIntEnvWithDefault("TEST_INT_VAR", 10)
	assert.Equal(t, 10, result)
}
