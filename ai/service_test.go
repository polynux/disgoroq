package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				PrimaryProvider:   ProviderGroq,
				GroqAPIKey:        "test-key",
				GroqModel:         "openai/gpt-oss-20b",
				GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: false,
		},
		{
			name: "missing groq api key",
			config: ServiceConfig{
				PrimaryProvider:   ProviderGroq,
				GroqAPIKey:        "",
				GroqModel:         "openai/gpt-oss-20b",
				GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "GROQ_API_KEY is required when groq is the primary provider",
		},
		{
			name: "valid ollama primary config",
			config: ServiceConfig{
				PrimaryProvider:   ProviderOllama,
				OllamaEnabled:     true,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "dolphin3",
				OllamaVisionModel: "llava",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: false,
		},
		{
			name: "valid opencode primary config",
			config: ServiceConfig{
				PrimaryProvider:     ProviderOpencode,
				OpencodeEnabled:     true,
				OpencodeBaseURL:     "https://opencode.ai/zen/go/v1",
				OpencodeAPIKey:      "test-key",
				OpencodeModel:       "deepseek-v4-flash",
				OpencodeVisionModel: "deepseek-v4-flash",
				MinResponseLength:   1,
				RetryConfig:         DefaultRetryConfig(),
			},
			shouldError: false,
		},
		{
			name: "ollama primary requires enabled provider",
			config: ServiceConfig{
				PrimaryProvider:   ProviderOllama,
				OllamaEnabled:     false,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "dolphin3",
				OllamaVisionModel: "llava",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "ollama must be enabled when ollama is the primary provider",
		},
		{
			name: "invalid min response length",
			config: ServiceConfig{
				PrimaryProvider:   ProviderGroq,
				GroqAPIKey:        "test-key",
				GroqModel:         "openai/gpt-oss-20b",
				GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
				MinResponseLength: 0,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "minimum response length must be at least 1",
		},
		{
			name: "invalid retry config",
			config: ServiceConfig{
				PrimaryProvider:   ProviderGroq,
				GroqAPIKey:        "test-key",
				GroqModel:         "openai/gpt-oss-20b",
				GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
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
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
		OllamaEnabled:     false, // Disable Ollama to avoid connection issues
		OllamaURL:         "http://localhost:11434",
		OllamaModel:       "dolphin3",
		OllamaVisionModel: "llava",
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
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "", // No API key
		OllamaEnabled:     false,
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
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
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
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
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
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
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
		OllamaEnabled:     true,
		OllamaURL:         "http://ollama.local:11434",
		OllamaModel:       "llama2",
		OllamaVisionModel: "llava",
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 5,
		FallbackEnabled:   true,
	}

	service := NewService(config)
	info := service.GetProviderInfo()

	assert.NotNil(t, info)
	assert.Equal(t, ProviderGroq, info["primary_provider"])
	assert.Equal(t, true, info["fallback_enabled"])
	assert.Equal(t, true, info["groq_enabled"])
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
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	service1 := NewService(config1)
	assert.False(t, service1.IsFallbackAvailable())

	// Test with fallback enabled but Ollama disabled
	config2 := ServiceConfig{
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
		OllamaEnabled:     false,
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   true,
	}

	service2 := NewService(config2)
	assert.False(t, service2.IsFallbackAvailable())

	// Test with fallback enabled and Ollama enabled
	config3 := ServiceConfig{
		PrimaryProvider:   ProviderGroq,
		GroqAPIKey:        "test-key-123",
		GroqModel:         "openai/gpt-oss-20b",
		GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
		OllamaEnabled:     true,
		OllamaURL:         "http://localhost:11434",
		OllamaModel:       "dolphin3",
		OllamaVisionModel: "llava",
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

func TestNewServiceWithOllamaPrimary(t *testing.T) {
	config := ServiceConfig{
		PrimaryProvider:   ProviderOllama,
		OllamaEnabled:     true,
		OllamaURL:         "http://localhost:11434",
		OllamaModel:       "dolphin3",
		OllamaVisionModel: "llava",
		RetryConfig:       DefaultRetryConfig(),
		MinResponseLength: 1,
		FallbackEnabled:   false,
	}

	service := NewService(config)

	assert.NotNil(t, service)
	assert.Equal(t, ProviderOllama, service.Name())
	assert.Equal(t, ProviderOllama, service.GetProviderInfo()["primary_provider"])
}

func TestNewServiceWithOpencodePrimary(t *testing.T) {
	config := ServiceConfig{
		PrimaryProvider:     ProviderOpencode,
		OpencodeEnabled:     true,
		OpencodeBaseURL:     "https://opencode.ai/zen/go/v1",
		OpencodeAPIKey:      "test-key",
		OpencodeModel:       "deepseek-v4-flash",
		OpencodeVisionModel: "deepseek-v4-flash",
		RetryConfig:         DefaultRetryConfig(),
		MinResponseLength:   1,
		FallbackEnabled:     false,
	}

	service := NewService(config)

	assert.NotNil(t, service)
	assert.Equal(t, ProviderOpencode, service.Name())
	assert.Equal(t, ProviderOpencode, service.GetProviderInfo()["primary_provider"])
}
