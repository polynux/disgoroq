package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroqBuildMessagesInlinesReferencedImages(t *testing.T) {
	req := &ChatRequest{
		SystemPrompt: "system prompt",
		Messages: []Message{
			{
				Role:      "user",
				Content:   "describe this",
				ImageRefs: []int{0, 1},
			},
			{
				Role:    "assistant",
				Content: "done",
			},
		},
		Images: []ImageContext{
			{URL: "https://example.com/one.png"},
			{URL: "https://example.com/two.png"},
		},
	}

	messages := buildGroqMessages(req)

	require.Len(t, messages, 3)
	assert.Equal(t, RoleSystem, messages[0].Role)
	assert.Equal(t, "system prompt", messages[0].Content)

	assert.Equal(t, RoleUser, messages[1].Role)
	parts, ok := messages[1].Content.([]openAIMessageContentPart)
	require.True(t, ok)
	require.Len(t, parts, 3)
	assert.Equal(t, "text", parts[0].Type)
	assert.Equal(t, "describe this", parts[0].Text)
	assert.Equal(t, "image_url", parts[1].Type)
	assert.Equal(t, "https://example.com/one.png", parts[1].ImageURL.URL)
	assert.Equal(t, "image_url", parts[2].Type)
	assert.Equal(t, "https://example.com/two.png", parts[2].ImageURL.URL)

	assert.Equal(t, RoleAssistant, messages[2].Role)
	assert.Equal(t, "done", messages[2].Content)
}

func TestGroqBuildMessagesPrefersMaterializedImageURLOverSourceURL(t *testing.T) {
	req := &ChatRequest{
		Messages: []Message{{
			Role:      "user",
			Content:   "describe this",
			ImageRefs: []int{0},
		}},
		Images: []ImageContext{{
			URL:       "data:image/png;base64,ZmFrZS1pbWFnZS1ieXRlcw==",
			SourceURL: "https://media.discordapp.net/attachments/example.png",
		}},
	}

	messages := buildGroqMessages(req)

	require.Len(t, messages, 1)
	parts, ok := messages[0].Content.([]openAIMessageContentPart)
	require.True(t, ok)
	require.Len(t, parts, 2)
	require.NotNil(t, parts[1].ImageURL)
	assert.Equal(t, "data:image/png;base64,ZmFrZS1pbWFnZS1ieXRlcw==", parts[1].ImageURL.URL)
	assert.NotEqual(t, req.Images[0].SourceURL, parts[1].ImageURL.URL)
}

func TestGroqSupportsInlineImagesOnlyForSharedScoutModel(t *testing.T) {
	provider := NewGroqProvider("test-key", true)

	assert.True(t, provider.SupportsInlineImages(
		"meta-llama/llama-4-scout-17b-16e-instruct",
		"meta-llama/llama-4-scout-17b-16e-instruct",
	))
	assert.False(t, provider.SupportsInlineImages(
		"openai/gpt-oss-20b",
		"meta-llama/llama-4-scout-17b-16e-instruct",
	))
	assert.False(t, provider.SupportsInlineImages(
		"meta-llama/llama-4-scout-17b-16e-instruct",
		"openai/gpt-oss-20b",
	))
}

func TestGroqReasoningEffortDisablesSupportedModels(t *testing.T) {
	assert.Equal(t, "low", groqReasoningEffort("openai/gpt-oss-20b", false))
	assert.Equal(t, "none", groqReasoningEffort("qwen/qwen3-32b", false))
	assert.Equal(t, "", groqReasoningEffort("meta-llama/llama-4-scout-17b-16e-instruct", false))
	assert.Equal(t, "", groqReasoningEffort("openai/gpt-oss-20b", true))
}

func TestGroqChatParsesPromptCacheMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"model":"openai/gpt-oss-20b"`)
		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"openai/gpt-oss-20b","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":20,"total_tokens":100,"prompt_tokens_details":{"cached_tokens":64}}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider := NewGroqProvider("test-key", false)
	provider.baseURL = server.URL

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "openai/gpt-oss-20b",
		Messages: []Message{{
			Role:    "user",
			Content: "hello",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hi", resp.Content)
	assert.Equal(t, 100, resp.TokensUsed)
	assert.Equal(t, 80, resp.PromptTokens)
	assert.Equal(t, 20, resp.CompletionTokens)
	assert.Equal(t, 64, resp.CachedTokens)
}

func TestGroqChatParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"tools":[`)
		assert.Contains(t, string(body), `"tool_choice":"auto"`)
		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"openai/gpt-oss-20b","choices":[{"message":{"content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30,"prompt_tokens_details":{"cached_tokens":0}}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider := NewGroqProvider("test-key", false)
	provider.baseURL = server.URL

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "openai/gpt-oss-20b",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "hello",
		}},
		Tools: []ToolDefinition{{
			Function: ToolFunctionDefinition{
				Name:        defaultWebToolName,
				Description: "fetch web pages",
				Parameters: ToolSchema{
					Type: "object",
					Properties: map[string]ToolProperty{
						"url": {Type: "string"},
					},
					Required: []string{"url"},
				},
			},
		}},
		ToolChoice: &ToolChoice{Mode: ToolChoiceAuto},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, FinishReasonToolCalls, resp.FinishReason)
	assert.Equal(t, "web_fetch", resp.ToolCalls[0].Function.Name)
}
