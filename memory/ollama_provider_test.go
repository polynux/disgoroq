package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOllamaEmbeddingProvider tests the Ollama embedding provider
func TestOllamaEmbeddingProvider(t *testing.T) {
	t.Run("NewOllamaEmbeddingProvider_Defaults", func(t *testing.T) {
		provider, err := NewOllamaEmbeddingProvider()
		assert.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, "http://localhost:11434", provider.GetBaseURL())
		assert.Equal(t, "nomic-embed-text", provider.GetModel())
		assert.Equal(t, 30*time.Second, provider.httpClient.Timeout)
	})

	t.Run("NewOllamaEmbeddingProviderWithConfig", func(t *testing.T) {
		provider, err := NewOllamaEmbeddingProviderWithConfig("http://custom:8080", "custom-model")
		assert.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, "http://custom:8080", provider.GetBaseURL())
		assert.Equal(t, "custom-model", provider.GetModel())
	})

	t.Run("GenerateEmbedding_Success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/embed", r.URL.Path)
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var req OllamaEmbedRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			assert.NoError(t, err)
			assert.Equal(t, "nomic-embed-text", req.Model)
			assert.Equal(t, "test text", req.Input)
			assert.True(t, req.Truncate)

			embedding := make([]float32, 768)
			for i := 0; i < 768; i++ {
				embedding[i] = float32(i) / 100.0
			}

			resp := OllamaEmbedResponse{
				Model:           "nomic-embed-text",
				Embeddings:      [][]float32{embedding},
				TotalDuration:   1000000,
				LoadDuration:    500000,
				PromptEvalCount: 2,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		embedding, err := provider.GenerateEmbedding(context.Background(), "test text")
		assert.NoError(t, err)
		assert.NotNil(t, embedding)
		assert.Len(t, embedding, 768)

		for i, val := range embedding {
			assert.InDelta(t, float32(i)/100.0, val, 0.001)
		}
	})

	t.Run("GenerateEmbedding_EmptyText", func(t *testing.T) {
		provider, err := NewOllamaEmbeddingProvider()
		require.NoError(t, err)

		embedding, err := provider.GenerateEmbedding(context.Background(), "")
		assert.Error(t, err)
		assert.Nil(t, embedding)
		assert.Contains(t, err.Error(), "text cannot be empty")
	})

	t.Run("GenerateEmbedding_HTTPError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(OllamaErrorResponse{Error: "internal server error"})
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		embedding, err := provider.GenerateEmbedding(context.Background(), "test text")
		assert.Error(t, err)
		assert.Nil(t, embedding)
		assert.Contains(t, err.Error(), "Ollama API error: internal server error")
	})

	t.Run("GenerateEmbedding_InvalidResponse", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		embedding, err := provider.GenerateEmbedding(context.Background(), "test text")
		assert.Error(t, err)
		assert.Nil(t, embedding)
		assert.Contains(t, err.Error(), "failed to unmarshal response")
	})

	t.Run("GenerateEmbedding_NoEmbeddings", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := OllamaEmbedResponse{
				Model:           "nomic-embed-text",
				Embeddings:      [][]float32{}, // Empty embeddings
				TotalDuration:   1000000,
				LoadDuration:    500000,
				PromptEvalCount: 2,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		embedding, err := provider.GenerateEmbedding(context.Background(), "test text")
		assert.Error(t, err)
		assert.Nil(t, embedding)
		assert.Contains(t, err.Error(), "no embeddings returned from Ollama")
	})

	t.Run("GenerateEmbedding_WrongDimensions", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			embedding := make([]float32, 384)
			resp := OllamaEmbedResponse{
				Model:           "nomic-embed-text",
				Embeddings:      [][]float32{embedding},
				TotalDuration:   1000000,
				LoadDuration:    500000,
				PromptEvalCount: 2,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		embedding, err := provider.GenerateEmbedding(context.Background(), "test text")
		assert.Error(t, err)
		assert.Nil(t, embedding)
		assert.Contains(t, err.Error(), "unexpected embedding dimension: expected 768, got 384")
	})

	t.Run("HealthCheck_Success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/tags" {
				resp := struct {
					Models []struct {
						Name string `json:"name"`
					} `json:"models"`
				}{
					Models: []struct {
						Name string `json:"name"`
					}{
						{Name: "nomic-embed-text"},
						{Name: "llama2"},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		err = provider.HealthCheck(context.Background())
		assert.NoError(t, err)
	})

	t.Run("HealthCheck_ModelNotFound", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}{
				Models: []struct {
					Name string `json:"name"`
				}{
					{Name: "llama2"}, // nomic-embed-text not available
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		err = provider.HealthCheck(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "model 'nomic-embed-text' not found")
		assert.Contains(t, err.Error(), "ollama pull nomic-embed-text")
	})

	t.Run("HealthCheck_ConnectionFailed", func(t *testing.T) {
		// Use an invalid URL to simulate connection failure
		provider, err := NewOllamaEmbeddingProviderWithConfig("http://invalid-host:99999", "nomic-embed-text")
		require.NoError(t, err)

		err = provider.HealthCheck(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to connect to Ollama")
	})

	t.Run("SetTimeout", func(t *testing.T) {
		provider, err := NewOllamaEmbeddingProvider()
		require.NoError(t, err)

		newTimeout := 10 * time.Second
		provider.SetTimeout(newTimeout)
		assert.Equal(t, newTimeout, provider.httpClient.Timeout)
	})

	t.Run("GenerateEmbeddingBatch", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++

			embedding := make([]float32, 768)
			for i := 0; i < 768; i++ {
				embedding[i] = float32(callCount*100 + i)
			}

			resp := OllamaEmbedResponse{
				Model:           "nomic-embed-text",
				Embeddings:      [][]float32{embedding},
				TotalDuration:   1000000,
				LoadDuration:    500000,
				PromptEvalCount: 2,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		texts := []string{"text 1", "text 2", "text 3"}
		embeddings, err := provider.GenerateEmbeddingBatch(context.Background(), texts)
		assert.NoError(t, err)
		assert.Len(t, embeddings, 3)
		assert.Equal(t, 3, callCount) // Should make 3 separate calls

		// Verify each embedding is different
		for i, embedding := range embeddings {
			assert.Len(t, embedding, 768)
			// First value should reflect the call count
			assert.InDelta(t, float32((i+1)*100), embedding[0], 0.1)
		}
	})

	t.Run("EstimateTokens", func(t *testing.T) {
		tests := []struct {
			text     string
			expected int
		}{
			{"hello", 1},                     // 5 chars / 4 = 1.25 -> 1
			{"hello world", 2},               // 11 chars / 4 = 2.75 -> 2
			{"this is a longer sentence", 6}, // 27 chars / 4 = 6.75 -> 6
			{"", 0},                          // empty text
		}

		for _, tt := range tests {
			tokens := EstimateTokens(tt.text)
			assert.Equal(t, tt.expected, tokens)
		}
	})

	t.Run("ValidateTextLength", func(t *testing.T) {
		provider, err := NewOllamaEmbeddingProvider()
		require.NoError(t, err)

		// Short text should be valid
		err = provider.ValidateTextLength("short text")
		assert.NoError(t, err)

		// Very long text should fail
		longText := string(make([]byte, 40000)) // ~10k tokens
		err = provider.ValidateTextLength(longText)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "text too long")
		assert.Contains(t, err.Error(), "estimated 10000 tokens, maximum 8192 tokens")
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate slow response
			time.Sleep(100 * time.Millisecond)

			embedding := make([]float32, 768)
			resp := OllamaEmbedResponse{
				Model:           "nomic-embed-text",
				Embeddings:      [][]float32{embedding},
				TotalDuration:   1000000,
				LoadDuration:    500000,
				PromptEvalCount: 2,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		provider, err := NewOllamaEmbeddingProviderWithConfig(server.URL, "nomic-embed-text")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())

		// Cancel context immediately
		cancel()

		embedding, err := provider.GenerateEmbedding(ctx, "test text")
		assert.Error(t, err)
		assert.Nil(t, embedding)
		assert.Contains(t, err.Error(), "context canceled")
	})
}

// TestOllamaProviderIntegration tests with a real Ollama instance (if available)
func TestOllamaProviderIntegration(t *testing.T) {
	// Skip this test by default - only run if Ollama is known to be available
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	provider, err := NewOllamaEmbeddingProvider()
	if err != nil {
		t.Skip("Failed to create Ollama provider:", err)
	}

	// Check if Ollama is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = provider.HealthCheck(ctx)
	if err != nil {
		t.Skip("Ollama not available or model not pulled:", err)
	}

	t.Run("RealEmbeddingGeneration", func(t *testing.T) {
		embedding, err := provider.GenerateEmbedding(context.Background(), "Hello, this is a test sentence for embedding generation.")
		assert.NoError(t, err)
		assert.NotNil(t, embedding)
		assert.Len(t, embedding, 768)

		// Verify embedding is valid (no NaN or Inf values)
		for i, val := range embedding {
			assert.False(t, isNaNOrInf(val), "Embedding contains invalid value at index %d: %f", i, val)
		}

		// Test with different text
		embedding2, err := provider.GenerateEmbedding(context.Background(), "This is a completely different sentence with different meaning.")
		assert.NoError(t, err)
		assert.NotNil(t, embedding2)
		assert.Len(t, embedding2, 768)

		// Embeddings should be different
		assert.NotEqual(t, embedding, embedding2)
	})

	t.Run("RealBatchEmbedding", func(t *testing.T) {
		texts := []string{
			"The quick brown fox jumps over the lazy dog.",
			"Machine learning is a subset of artificial intelligence.",
			"Go is a statically typed, compiled programming language.",
		}

		embeddings, err := provider.GenerateEmbeddingBatch(context.Background(), texts)
		assert.NoError(t, err)
		assert.Len(t, embeddings, 3)

		for i, embedding := range embeddings {
			assert.Len(t, embedding, 768, "Embedding %d has wrong dimensions", i)
			// Verify no invalid values
			for j, val := range embedding {
				assert.False(t, isNaNOrInf(val), "Embedding %d contains invalid value at index %d: %f", i, j, val)
			}
		}
	})

	t.Run("RealTextValidation", func(t *testing.T) {
		// Valid text
		err := provider.ValidateTextLength("This is a normal length text that should be fine for embedding.")
		assert.NoError(t, err)

		// Very long text (should still be valid for nomic-embed-text)
		longText := "This is a very long text. " + string(make([]byte, 20000)) // ~5k tokens
		err = provider.ValidateTextLength(longText)
		// Should either pass or fail gracefully
		if err != nil {
			assert.Contains(t, err.Error(), "text too long")
		}
	})
}

// Helper function to check for NaN or Inf values
func isNaNOrInf(f float32) bool {
	return f != f || (f > 0 && f*2 == f) || (f < 0 && f*2 == f)
}
