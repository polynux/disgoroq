package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpencodeChatSendsBearerRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req opencodeChatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		assert.Equal(t, "deepseek-v4-flash", req.Model)
		assert.Equal(t, "none", req.ReasoningEffort)
		require.Len(t, req.Messages, 1)
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, "hello", req.Messages[0].Content)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"deepseek-v4-flash","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":12,"total_tokens":42,"prompt_tokens_details":{"cached_tokens":24,"cache_write_tokens":6}}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpencodeProvider(server.URL, "test-key", false)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []Message{{
			Role:    "user",
			Content: "hello",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hi", resp.Content)
	assert.Equal(t, "deepseek-v4-flash", resp.Model)
	assert.Equal(t, 42, resp.TokensUsed)
	assert.Equal(t, 30, resp.PromptTokens)
	assert.Equal(t, 12, resp.CompletionTokens)
	assert.Equal(t, 24, resp.CachedTokens)
	assert.Equal(t, 6, resp.CacheWriteTokens)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestOpencodeVisionSendsMultipartContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req opencodeChatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		require.Len(t, req.Messages, 1)

		partsData, err := json.Marshal(req.Messages[0].Content)
		require.NoError(t, err)

		var parts []opencodeMessageContentPart
		err = json.Unmarshal(partsData, &parts)
		require.NoError(t, err)

		require.Len(t, parts, 2)
		assert.Equal(t, "text", parts[0].Type)
		assert.Equal(t, "describe", parts[0].Text)
		assert.Equal(t, "image_url", parts[1].Type)
		require.NotNil(t, parts[1].ImageURL)
		assert.Equal(t, "https://example.com/cat.png", parts[1].ImageURL.URL)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"deepseek-v4-flash","choices":[{"message":{"content":"cat"},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":8,"total_tokens":26,"prompt_tokens_details":{"cached_tokens":10,"cache_write_tokens":4}}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpencodeProvider(server.URL, "test-key", true)
	require.NoError(t, err)

	resp, err := provider.Vision(context.Background(), &VisionRequest{
		Model:       "deepseek-v4-flash",
		Instruction: "describe",
		ImageURL:    "https://example.com/cat.png",
	})
	require.NoError(t, err)
	assert.Equal(t, "cat", resp.Description)
	assert.Equal(t, "deepseek-v4-flash", resp.Model)
	assert.Equal(t, 26, resp.TokensUsed)
	assert.Equal(t, 18, resp.PromptTokens)
	assert.Equal(t, 8, resp.CompletionTokens)
	assert.Equal(t, 10, resp.CachedTokens)
	assert.Equal(t, 4, resp.CacheWriteTokens)
}
