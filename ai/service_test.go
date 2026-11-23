package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
			name: "valid openrouter primary config",
			config: ServiceConfig{
				PrimaryProvider:       ProviderOpenrouter,
				OpenrouterEnabled:     true,
				OpenrouterBaseURL:     "https://openrouter.ai/api/v1",
				OpenrouterAPIKey:      "test-key",
				OpenrouterModel:       "google/gemini-2.5-flash",
				OpenrouterVisionModel: "google/gemini-2.5-flash",
				MinResponseLength:     1,
				RetryConfig:           DefaultRetryConfig(),
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
			name: "ollama primary with empty vision_provider still requires ollama vision model",
			config: ServiceConfig{
				PrimaryProvider:   ProviderOllama,
				VisionProvider:    "",
				OllamaEnabled:     true,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "luciole",
				OllamaVisionModel: "",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "Ollama vision model is required when ollama is the primary provider",
		},
		{
			name: "ollama primary with opencode vision_provider allows empty ollama vision model",
			config: ServiceConfig{
				PrimaryProvider:     ProviderOllama,
				VisionProvider:      ProviderOpencode,
				OllamaEnabled:       true,
				OllamaURL:           "http://localhost:11434",
				OllamaModel:         "luciole",
				OllamaVisionModel:   "",
				OpencodeEnabled:     true,
				OpencodeBaseURL:     "https://opencode.ai/inference/go/v1",
				OpencodeAPIKey:      "opencode-key",
				OpencodeModel:       "deepseek-v4.1-flash",
				OpencodeVisionModel: "deepseek-v4.1-flash",
				MinResponseLength:   1,
				RetryConfig:         DefaultRetryConfig(),
			},
			shouldError: false,
		},
		{
			name: "unknown vision_provider rejected",
			config: ServiceConfig{
				PrimaryProvider:   ProviderGroq,
				VisionProvider:    "unknown",
				GroqAPIKey:        "test-key",
				GroqModel:         "openai/gpt-oss-20b",
				GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    `unsupported vision provider "unknown"`,
		},
		{
			name: "disabled opencode vision_provider rejected",
			config: ServiceConfig{
				PrimaryProvider:   ProviderOllama,
				VisionProvider:    ProviderOpencode,
				OllamaEnabled:     true,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "luciole",
				OpencodeEnabled:   false,
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "opencode must be enabled when opencode is the vision provider",
		},
		{
			name: "opencode vision_provider requires vision model",
			config: ServiceConfig{
				PrimaryProvider:     ProviderOllama,
				VisionProvider:      ProviderOpencode,
				OllamaEnabled:       true,
				OllamaURL:           "http://localhost:11434",
				OllamaModel:         "luciole",
				OpencodeEnabled:     true,
				OpencodeBaseURL:     "https://opencode.ai/inference/go/v1",
				OpencodeAPIKey:      "opencode-key",
				OpencodeModel:       "deepseek-v4.1-flash",
				OpencodeVisionModel: "",
				MinResponseLength:   1,
				RetryConfig:         DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "OpenCode vision model is required when opencode is the vision provider",
		},
		{
			name: "ollama vision_provider requires ollama vision model",
			config: ServiceConfig{
				PrimaryProvider:   ProviderGroq,
				VisionProvider:    ProviderOllama,
				GroqAPIKey:        "test-key",
				GroqModel:         "openai/gpt-oss-20b",
				GroqVisionModel:   "meta-llama/llama-4-scout-17b-16e-instruct",
				OllamaEnabled:     true,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "luciole",
				OllamaVisionModel: "",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "Ollama vision model is required when ollama is the vision provider",
		},
		{
			name: "groq vision_provider requires api key",
			config: ServiceConfig{
				PrimaryProvider:   ProviderOllama,
				VisionProvider:    ProviderGroq,
				OllamaEnabled:     true,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "luciole",
				GroqAPIKey:        "",
				GroqVisionModel:   "scout",
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "GROQ_API_KEY is required when groq is the vision provider",
		},
		{
			name: "openrouter vision_provider requires enabled provider",
			config: ServiceConfig{
				PrimaryProvider:   ProviderOllama,
				VisionProvider:    ProviderOpenrouter,
				OllamaEnabled:     true,
				OllamaURL:         "http://localhost:11434",
				OllamaModel:       "luciole",
				OpenrouterEnabled: false,
				MinResponseLength: 1,
				RetryConfig:       DefaultRetryConfig(),
			},
			shouldError: true,
			errorMsg:    "openrouter must be enabled when openrouter is the vision provider",
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
		OpenrouterEnabled: false,
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
	assert.Equal(t, false, info["openrouter_enabled"])
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

func TestNewServiceWithOpenrouterPrimary(t *testing.T) {
	config := ServiceConfig{
		PrimaryProvider:       ProviderOpenrouter,
		OpenrouterEnabled:     true,
		OpenrouterBaseURL:     "https://openrouter.ai/api/v1",
		OpenrouterAPIKey:      "test-key",
		OpenrouterModel:       "google/gemini-2.5-flash",
		OpenrouterVisionModel: "google/gemini-2.5-flash",
		RetryConfig:           DefaultRetryConfig(),
		MinResponseLength:     1,
		FallbackEnabled:       false,
	}

	service := NewService(config)

	assert.NotNil(t, service)
	assert.Equal(t, ProviderOpenrouter, service.Name())
	assert.Equal(t, ProviderOpenrouter, service.GetProviderInfo()["primary_provider"])
}

const testInlinePNGDataURI = "data:image/png;base64,iVBORw0KGgo="

func noRetryTestConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    0,
		InitialDelay:  time.Millisecond,
		MaxDelay:      2 * time.Millisecond,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}
}

type recordedProviderCall struct {
	Provider string
	Model    string
	Body     string
}

func newFakeOllamaServer(t *testing.T, mu *sync.Mutex, calls *[]recordedProviderCall, handler func(model, body string) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &payload)

		mu.Lock()
		*calls = append(*calls, recordedProviderCall{Provider: ProviderOllama, Model: payload.Model, Body: string(raw)})
		mu.Unlock()

		status, content := handler(payload.Model, string(raw))
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"` + content + `"}`))
			return
		}
		resp := map[string]any{
			"model":       payload.Model,
			"message":     map[string]any{"role": "assistant", "content": content},
			"done":        true,
			"done_reason": "stop",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newFakeOpenAIServer(t *testing.T, providerName string, mu *sync.Mutex, calls *[]recordedProviderCall, handler func(model, body string) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &payload)

		mu.Lock()
		*calls = append(*calls, recordedProviderCall{Provider: providerName, Model: payload.Model, Body: string(raw)})
		mu.Unlock()

		status, content := handler(payload.Model, string(raw))
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"` + content + `"}}`))
			return
		}
		resp := map[string]any{
			"model": payload.Model,
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": content},
					"finish_reason": "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestServiceVisionRoutingOrderAndFallback(t *testing.T) {
	t.Run("tries configured vision_provider first with its vision_model", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "ollama vision"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "opencode vision description"
		})
		defer opencodeSrv.Close()

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter vision"
		})
		defer openrouterSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOllama,
			VisionProvider:        ProviderOpencode,
			OllamaEnabled:         true,
			OllamaURL:             ollamaSrv.URL,
			OllamaModel:           "luciole",
			OllamaVisionModel:     "llava",
			OpencodeEnabled:       true,
			OpencodeBaseURL:       opencodeSrv.URL,
			OpencodeAPIKey:        "oc-key",
			OpencodeModel:         "deepseek-v4-pro",
			OpencodeVisionModel:   "deepseek-v4.1-flash",
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       true,
		})
		require.NotNil(t, service)
		assert.Equal(t, ProviderOpencode, service.GetProviderInfo()["vision_provider"])

		resp, err := service.Vision(context.Background(), &VisionRequest{
			Instruction: "Describe",
			ImageURL:    testInlinePNGDataURI,
		})
		require.NoError(t, err)
		assert.Equal(t, "opencode vision description", resp.Description)
		assert.Equal(t, ProviderOpencode, resp.Provider)
		assert.Equal(t, "deepseek-v4.1-flash", resp.Model)

		require.Len(t, calls, 1)
		assert.Equal(t, ProviderOpencode, calls[0].Provider)
		assert.Equal(t, "deepseek-v4.1-flash", calls[0].Model)
	})

	t.Run("falls back in usual order without calling vision_provider twice", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusPaymentRequired, "payment required"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusServiceUnavailable, "opencode down"
		})
		defer opencodeSrv.Close()

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter fallback vision"
		})
		defer openrouterSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOllama,
			VisionProvider:        ProviderOpencode,
			OllamaEnabled:         true,
			OllamaURL:             ollamaSrv.URL,
			OllamaModel:           "luciole",
			OllamaVisionModel:     "llava",
			OpencodeEnabled:       true,
			OpencodeBaseURL:       opencodeSrv.URL,
			OpencodeAPIKey:        "oc-key",
			OpencodeModel:         "deepseek-v4-pro",
			OpencodeVisionModel:   "deepseek-v4.1-flash",
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       true,
		})
		require.NotNil(t, service)

		resp, err := service.Vision(context.Background(), &VisionRequest{
			Instruction: "Describe",
			ImageURL:    testInlinePNGDataURI,
		})
		require.NoError(t, err)
		assert.Equal(t, "openrouter fallback vision", resp.Description)
		assert.Equal(t, ProviderOpenrouter, resp.Provider)

		require.Len(t, calls, 3)
		assert.Equal(t, []string{ProviderOpencode, ProviderOllama, ProviderOpenrouter}, []string{
			calls[0].Provider, calls[1].Provider, calls[2].Provider,
		})
		assert.Equal(t, "deepseek-v4.1-flash", calls[0].Model)
		assert.Equal(t, "llava", calls[1].Model)
		assert.Equal(t, "google/gemini-2.5-flash", calls[2].Model)
	})

	t.Run("skips ollama in vision fallback when ollama vision_model is empty", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "should not be called"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusServiceUnavailable, "opencode down"
		})
		defer opencodeSrv.Close()

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter rescued"
		})
		defer openrouterSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOllama,
			VisionProvider:        ProviderOpencode,
			OllamaEnabled:         true,
			OllamaURL:             ollamaSrv.URL,
			OllamaModel:           "luciole",
			OllamaVisionModel:     "",
			OpencodeEnabled:       true,
			OpencodeBaseURL:       opencodeSrv.URL,
			OpencodeAPIKey:        "oc-key",
			OpencodeModel:         "deepseek-v4-pro",
			OpencodeVisionModel:   "deepseek-v4.1-flash",
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       true,
		})
		require.NotNil(t, service)

		resp, err := service.Vision(context.Background(), &VisionRequest{
			Instruction: "Describe",
			ImageURL:    testInlinePNGDataURI,
		})
		require.NoError(t, err)
		assert.Equal(t, "openrouter rescued", resp.Description)

		require.Len(t, calls, 2)
		assert.Equal(t, []string{ProviderOpencode, ProviderOpenrouter}, []string{calls[0].Provider, calls[1].Provider})
	})

	t.Run("empty vision_provider preserves old routing order starting with primary", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusPaymentRequired, "payment required"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "opencode fallback"
		})
		defer opencodeSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:     ProviderOllama,
			VisionProvider:      "",
			OllamaEnabled:       true,
			OllamaURL:           ollamaSrv.URL,
			OllamaModel:         "luciole",
			OllamaVisionModel:   "llava",
			OpencodeEnabled:     true,
			OpencodeBaseURL:     opencodeSrv.URL,
			OpencodeAPIKey:      "oc-key",
			OpencodeModel:       "deepseek-v4.1-flash",
			OpencodeVisionModel: "deepseek-v4.1-flash",
			RetryConfig:         noRetryTestConfig(),
			MinResponseLength:   1,
			FallbackEnabled:     true,
		})
		require.NotNil(t, service)

		resp, err := service.Vision(context.Background(), &VisionRequest{
			Instruction: "Describe",
			ImageURL:    testInlinePNGDataURI,
		})
		require.NoError(t, err)
		assert.Equal(t, "opencode fallback", resp.Description)

		require.Len(t, calls, 2)
		assert.Equal(t, []string{ProviderOllama, ProviderOpencode}, []string{calls[0].Provider, calls[1].Provider})
	})
}

func TestServiceChatImageDescriptionsAndInlineBehavior(t *testing.T) {
	image := ImageContext{URL: testInlinePNGDataURI, Type: "image/png"}

	t.Run("ollama chat with opencode vision_provider describes via opencode and skips empty ollama vision_model", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "ollama chat reply"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "a sleepy cat"
		})
		defer opencodeSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:     ProviderOllama,
			VisionProvider:      ProviderOpencode,
			OllamaEnabled:       true,
			OllamaURL:           ollamaSrv.URL,
			OllamaModel:         "luciole",
			OllamaVisionModel:   "",
			OpencodeEnabled:     true,
			OpencodeBaseURL:     opencodeSrv.URL,
			OpencodeAPIKey:      "oc-key",
			OpencodeModel:       "deepseek-v4-pro",
			OpencodeVisionModel: "deepseek-v4.1-flash",
			RetryConfig:         noRetryTestConfig(),
			MinResponseLength:   1,
			FallbackEnabled:     false,
		})
		require.NotNil(t, service)
		assert.False(t, service.IsFallbackAvailable())
		assert.Equal(t, []AttachmentCacheScope{{Provider: ProviderOllama, Model: "luciole"}}, service.ChatCacheScopes())

		resp, err := service.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "what is this?", ImageRefs: []int{0}}},
			Images:   []ImageContext{image},
		})
		require.NoError(t, err)
		assert.Equal(t, "ollama chat reply", resp.Content)

		require.Len(t, calls, 2)
		assert.Equal(t, ProviderOpencode, calls[0].Provider)
		assert.Equal(t, "deepseek-v4.1-flash", calls[0].Model)
		assert.Equal(t, ProviderOllama, calls[1].Provider)
		assert.Equal(t, "luciole", calls[1].Model)
		assert.Contains(t, calls[1].Body, "a sleepy cat")
		assert.NotContains(t, calls[1].Body, `"images"`)
	})

	t.Run("different inline-capable chat provider and vision provider sends text description instead of inline image", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter text chat reply"
		})
		defer openrouterSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "described by opencode"
		})
		defer opencodeSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOpenrouter,
			VisionProvider:        ProviderOpencode,
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			OpencodeEnabled:       true,
			OpencodeBaseURL:       opencodeSrv.URL,
			OpencodeAPIKey:        "oc-key",
			OpencodeModel:         "deepseek-v4.1-flash",
			OpencodeVisionModel:   "deepseek-v4.1-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       true,
		})
		require.NotNil(t, service)

		resp, err := service.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "look", ImageRefs: []int{0}}},
			Images:   []ImageContext{image},
		})
		require.NoError(t, err)
		assert.Equal(t, "openrouter text chat reply", resp.Content)

		require.Len(t, calls, 2)
		assert.Equal(t, ProviderOpencode, calls[0].Provider)
		assert.Contains(t, calls[0].Body, `"image_url"`)
		assert.Equal(t, ProviderOpenrouter, calls[1].Provider)
		assert.Contains(t, calls[1].Body, "described by opencode")
		assert.NotContains(t, calls[1].Body, `"image_url"`)
	})

	t.Run("matching chat and vision provider keeps inline images without separate vision call", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "inline chat reply"
		})
		defer opencodeSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:     ProviderOpencode,
			VisionProvider:      ProviderOpencode,
			OpencodeEnabled:     true,
			OpencodeBaseURL:     opencodeSrv.URL,
			OpencodeAPIKey:      "oc-key",
			OpencodeModel:       "deepseek-v4.1-flash",
			OpencodeVisionModel: "deepseek-v4.1-flash",
			RetryConfig:         noRetryTestConfig(),
			MinResponseLength:   1,
			FallbackEnabled:     false,
		})
		require.NotNil(t, service)

		resp, err := service.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "look", ImageRefs: []int{0}}},
			Images:   []ImageContext{image},
		})
		require.NoError(t, err)
		assert.Equal(t, "inline chat reply", resp.Content)

		require.Len(t, calls, 1)
		assert.Equal(t, ProviderOpencode, calls[0].Provider)
		assert.Contains(t, calls[0].Body, `"image_url"`)
		assert.NotContains(t, calls[0].Body, "<IMAGE_DESC>")
	})

	t.Run("chat fallback reuses cached vision_provider description without calling vision twice", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall
		cache := newMemoryAttachmentCache()

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusServiceUnavailable, "ollama chat down"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			if model == "deepseek-v4-pro" {
				return http.StatusServiceUnavailable, "opencode chat down"
			}
			return http.StatusOK, "cached opencode cat"
		})
		defer opencodeSrv.Close()

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter chat rescued"
		})
		defer openrouterSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOllama,
			VisionProvider:        ProviderOpencode,
			OllamaEnabled:         true,
			OllamaURL:             ollamaSrv.URL,
			OllamaModel:           "luciole",
			OllamaVisionModel:     "",
			OpencodeEnabled:       true,
			OpencodeBaseURL:       opencodeSrv.URL,
			OpencodeAPIKey:        "oc-key",
			OpencodeModel:         "deepseek-v4-pro",
			OpencodeVisionModel:   "deepseek-v4.1-flash",
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       true,
			AttachmentCache:       cache,
		})
		require.NotNil(t, service)

		resp, err := service.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "look", ImageRefs: []int{0}}},
			Images:   []ImageContext{image},
		})
		require.NoError(t, err)
		assert.Equal(t, "openrouter chat rescued", resp.Content)

		// Expect exactly one vision call (opencode deepseek-v4.1-flash) followed by
		// chat attempts ollama -> opencode -> openrouter, with openrouter receiving
		// the cached description as text.
		visionCalls := 0
		for _, call := range calls {
			if call.Model == "deepseek-v4.1-flash" {
				visionCalls++
			}
		}
		assert.Equal(t, 1, visionCalls)
		require.Len(t, calls, 4)
		assert.Equal(t, ProviderOpenrouter, calls[3].Provider)
		assert.Contains(t, calls[3].Body, "cached opencode cat")
		assert.NotContains(t, calls[3].Body, `"image_url"`)

		entry, found, err := cache.GetAttachmentCache(context.Background(), attachmentCacheKey(imageDescriptionCacheInput(image), ProviderOpencode, "deepseek-v4.1-flash"))
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "cached opencode cat", entry.Content)
	})

	t.Run("chat image description falls back without calling vision_provider twice", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		ollamaSrv := newFakeOllamaServer(t, &mu, &calls, func(model, body string) (int, string) {
			if model == "llava" {
				return http.StatusPaymentRequired, "payment required"
			}
			return http.StatusOK, "ollama chat with fallback desc"
		})
		defer ollamaSrv.Close()

		opencodeSrv := newFakeOpenAIServer(t, ProviderOpencode, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusServiceUnavailable, "opencode vision down"
		})
		defer opencodeSrv.Close()

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter fallback image desc"
		})
		defer openrouterSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOllama,
			VisionProvider:        ProviderOpencode,
			OllamaEnabled:         true,
			OllamaURL:             ollamaSrv.URL,
			OllamaModel:           "luciole",
			OllamaVisionModel:     "llava",
			OpencodeEnabled:       true,
			OpencodeBaseURL:       opencodeSrv.URL,
			OpencodeAPIKey:        "oc-key",
			OpencodeModel:         "deepseek-v4-pro",
			OpencodeVisionModel:   "deepseek-v4.1-flash",
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       true,
		})
		require.NotNil(t, service)

		resp, err := service.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "look", ImageRefs: []int{0}}},
			Images:   []ImageContext{image},
		})
		require.NoError(t, err)
		assert.Equal(t, "ollama chat with fallback desc", resp.Content)

		// Vision order: opencode (fail) -> ollama (fail) -> openrouter (ok), then ollama chat.
		require.Len(t, calls, 4)
		assert.Equal(t, []string{ProviderOpencode, ProviderOllama, ProviderOpenrouter, ProviderOllama}, []string{
			calls[0].Provider, calls[1].Provider, calls[2].Provider, calls[3].Provider,
		})
		assert.Equal(t, "deepseek-v4.1-flash", calls[0].Model)
		assert.Equal(t, "llava", calls[1].Model)
		assert.Equal(t, "google/gemini-2.5-flash", calls[2].Model)
		assert.Equal(t, "luciole", calls[3].Model)
		assert.Contains(t, calls[3].Body, "openrouter fallback image desc")
	})

	t.Run("empty vision_provider preserves old inline images behavior on chat provider", func(t *testing.T) {
		var mu sync.Mutex
		var calls []recordedProviderCall

		openrouterSrv := newFakeOpenAIServer(t, ProviderOpenrouter, &mu, &calls, func(model, body string) (int, string) {
			return http.StatusOK, "openrouter inline reply"
		})
		defer openrouterSrv.Close()

		service := NewService(ServiceConfig{
			PrimaryProvider:       ProviderOpenrouter,
			VisionProvider:        "",
			OpenrouterEnabled:     true,
			OpenrouterBaseURL:     openrouterSrv.URL,
			OpenrouterAPIKey:      "or-key",
			OpenrouterModel:       "google/gemini-2.5-flash",
			OpenrouterVisionModel: "google/gemini-2.5-flash",
			RetryConfig:           noRetryTestConfig(),
			MinResponseLength:     1,
			FallbackEnabled:       false,
		})
		require.NotNil(t, service)

		resp, err := service.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: "user", Content: "look", ImageRefs: []int{0}}},
			Images:   []ImageContext{image},
		})
		require.NoError(t, err)
		assert.Equal(t, "openrouter inline reply", resp.Content)

		require.Len(t, calls, 1)
		assert.Equal(t, ProviderOpenrouter, calls[0].Provider)
		assert.Contains(t, calls[0].Body, `"image_url"`)
	})
}
