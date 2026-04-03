package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOllamaProvider(t *testing.T) {
	provider, err := NewOllamaProvider("http://localhost:11434")
	require.NoError(t, err)
	assert.NotNil(t, provider)
	assert.NotNil(t, provider.client)
}

func TestNewOllamaProvider_InvalidURL(t *testing.T) {
	provider, err := NewOllamaProvider("://bad-url")
	require.Error(t, err)
	assert.Nil(t, provider)
}

func TestOllamaChatAggregatesChunkedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/chat", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), `"num_predict":0`)

		w.Header().Set("Content-Type", "application/x-ndjson")
		_, err = io.WriteString(w, "{\"model\":\"dolphin3\",\"message\":{\"role\":\"assistant\",\"content\":\"hello \"},\"done\":false}\n")
		require.NoError(t, err)
		_, err = io.WriteString(w, "{\"model\":\"dolphin3\",\"message\":{\"role\":\"assistant\",\"content\":\"world\"},\"done\":false}\n")
		require.NoError(t, err)
		_, err = io.WriteString(w, "{\"model\":\"dolphin3\",\"message\":{\"role\":\"assistant\",\"content\":\"\"},\"done\":true,\"done_reason\":\"stop\",\"eval_count\":12,\"prompt_eval_count\":8}\n")
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model:       "dolphin3",
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Temperature: 0.2,
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp.Content)
	assert.Equal(t, "dolphin3", resp.Model)
	assert.Equal(t, 20, resp.TokensUsed)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestOllamaChatIncludesNumPredictWhenRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.True(t, strings.Contains(string(body), `"num_predict":128`))
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, err = io.WriteString(w, "{\"model\":\"dolphin3\",\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"done\":true,\"done_reason\":\"stop\"}\n")
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model:     "dolphin3",
		Messages:  []Message{{Role: "user", Content: "hi"}},
		MaxTokens: 128,
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}
