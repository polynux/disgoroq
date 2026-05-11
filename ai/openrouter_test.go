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

func TestOpenrouterChatSendsBearerRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req openrouterChatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		assert.Equal(t, "google/gemini-2.5-flash", req.Model)
		require.NotNil(t, req.Reasoning)
		assert.Equal(t, "none", req.Reasoning.Effort)
		assert.True(t, req.Reasoning.Exclude)
		require.Len(t, req.Messages, 1)
		assert.Equal(t, "user", req.Messages[0].Role)
		assert.Equal(t, "hello", req.Messages[0].Content)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"google/gemini-2.5-flash","choices":[{"message":{"content":"hi","reasoning":"hidden"},"finish_reason":"stop"}],"usage":{"total_tokens":12}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpenrouterProvider(server.URL, "test-key", false)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "google/gemini-2.5-flash",
		Messages: []Message{{
			Role:    "user",
			Content: "hello",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hi", resp.Content)
	assert.Equal(t, "google/gemini-2.5-flash", resp.Model)
	assert.Equal(t, 12, resp.TokensUsed)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestOpenrouterVisionSendsMultipartContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req openrouterChatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		require.Len(t, req.Messages, 1)

		partsData, err := json.Marshal(req.Messages[0].Content)
		require.NoError(t, err)

		var parts []openrouterMessageContentPart
		err = json.Unmarshal(partsData, &parts)
		require.NoError(t, err)

		require.Len(t, parts, 2)
		assert.Equal(t, "text", parts[0].Type)
		assert.Equal(t, "describe", parts[0].Text)
		assert.Equal(t, "image_url", parts[1].Type)
		require.NotNil(t, parts[1].ImageURL)
		assert.Equal(t, "https://example.com/cat.png", parts[1].ImageURL.URL)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"google/gemini-2.5-flash","choices":[{"message":{"content":"cat"},"finish_reason":"stop"}],"usage":{"total_tokens":8}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpenrouterProvider(server.URL, "test-key", true)
	require.NoError(t, err)

	resp, err := provider.Vision(context.Background(), &VisionRequest{
		Model:       "google/gemini-2.5-flash",
		Instruction: "describe",
		ImageURL:    "https://example.com/cat.png",
	})
	require.NoError(t, err)
	assert.Equal(t, "cat", resp.Description)
	assert.Equal(t, "google/gemini-2.5-flash", resp.Model)
	assert.Equal(t, 8, resp.TokensUsed)
}

func TestOpenrouterSupportsInlineImagesWhenModelsMatch(t *testing.T) {
	provider, err := NewOpenrouterProvider("https://openrouter.ai/api/v1", "test-key", true)
	require.NoError(t, err)

	assert.True(t, provider.SupportsInlineImages("google/gemini-2.5-flash", "google/gemini-2.5-flash"))
	assert.False(t, provider.SupportsInlineImages("google/gemini-2.5-flash", "openai/gpt-4.1-mini"))
}
