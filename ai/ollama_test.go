package ai

import (
	"context"
	"encoding/json"
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
		assert.Contains(t, string(body), `"think":false`)

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
		assert.Contains(t, string(body), `"think":false`)
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

func TestOllamaVisionSendsImageData(t *testing.T) {
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, err := io.WriteString(w, "fake-image-data")
		require.NoError(t, err)
	}))
	defer imageServer.Close()

	type chatMessage struct {
		Role    string   `json:"role"`
		Content string   `json:"content"`
		Images  []string `json:"images"`
	}
	type chatRequest struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
		Think    bool          `json:"think"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/chat", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req chatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)

		require.Len(t, req.Messages, 1)
		assert.Equal(t, "llava", req.Model)
		assert.False(t, req.Think)
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, "describe this image", req.Messages[0].Content)
		require.Len(t, req.Messages[0].Images, 1)
		assert.NotEmpty(t, req.Messages[0].Images[0])

		w.Header().Set("Content-Type", "application/x-ndjson")
		_, err = io.WriteString(w, "{\"model\":\"llava\",\"message\":{\"role\":\"assistant\",\"content\":\"a cat\"},\"done\":true,\"done_reason\":\"stop\",\"eval_count\":9,\"prompt_eval_count\":3}\n")
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL)
	require.NoError(t, err)

	resp, err := provider.Vision(context.Background(), &VisionRequest{
		Model:       "llava",
		Instruction: "describe this image",
		ImageURL:    imageServer.URL,
		MaxTokens:   64,
		Temperature: 0.1,
	})
	require.NoError(t, err)
	assert.Equal(t, "a cat", resp.Description)
	assert.Equal(t, "llava", resp.Model)
	assert.Equal(t, 12, resp.TokensUsed)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestOllamaChatSendsReferencedImages(t *testing.T) {
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, err := io.WriteString(w, "fake-image-data")
		require.NoError(t, err)
	}))
	defer imageServer.Close()

	type chatMessage struct {
		Role    string   `json:"role"`
		Content string   `json:"content"`
		Images  []string `json:"images"`
	}
	type chatRequest struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/chat", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req chatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)

		require.Len(t, req.Messages, 1)
		assert.Equal(t, "llava", req.Model)
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, "look", req.Messages[0].Content)
		require.Len(t, req.Messages[0].Images, 1)
		assert.NotEmpty(t, req.Messages[0].Images[0])

		w.Header().Set("Content-Type", "application/x-ndjson")
		_, err = io.WriteString(w, "{\"model\":\"llava\",\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"done\":true,\"done_reason\":\"stop\",\"eval_count\":5,\"prompt_eval_count\":4}\n")
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "llava",
		Messages: []Message{{
			Role:      "user",
			Content:   "look",
			ImageRefs: []int{0},
		}},
		Images: []ImageContext{{
			URL:  imageServer.URL,
			Type: "image/png",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestOllamaVisionErrorsOnImageDownloadFailure(t *testing.T) {
	provider, err := NewOllamaProvider("http://localhost:11434")
	require.NoError(t, err)

	_, err = provider.Vision(context.Background(), &VisionRequest{
		Model:       "llava",
		Instruction: "describe this image",
		ImageURL:    "://bad-url",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error downloading image")
}
