package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestGroqHTTPErrorsAreTypedAndClassified(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
		wantCalls   int32
	}{
		{
			name:        "404 model not found is final",
			status:      http.StatusNotFound,
			body:        `{"error":{"message":"The model llama-3.3-70b-versatile does not exist or you do not have access to it.","type":"invalid_request_error","code":"model_not_found"}}`,
			wantMessage: "model_not_found",
			wantCalls:   1,
		},
		{
			name:        "429 rate limit is retried despite the billing link",
			status:      http.StatusTooManyRequests,
			body:        `{"error":{"message":"Rate limit reached for model llama-3.3-70b-versatile. Please try again in 2.5s. Need more tokens? Upgrade to Dev Tier today at https://console.groq.com/settings/billing","type":"tokens","code":"rate_limit_exceeded"}}`,
			wantMessage: "console.groq.com/settings/billing",
			wantCalls:   3,
		},
		{
			name:        "503 is retried",
			status:      http.StatusServiceUnavailable,
			body:        `{"error":{"message":"service unavailable"}}`,
			wantMessage: "service unavailable",
			wantCalls:   3,
		},
		{
			name:        "401 is final",
			status:      http.StatusUnauthorized,
			body:        `{"error":{"message":"Invalid API Key"}}`,
			wantMessage: "Invalid API Key",
			wantCalls:   1,
		},
		{
			name:        "non-JSON error body is kept in the message",
			status:      http.StatusBadGateway,
			body:        `<html><body>Bad Gateway</body></html>`,
			wantMessage: "Bad Gateway",
			wantCalls:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			provider := NewGroqProvider("test-key", false)
			provider.baseURL = server.URL

			wrapper := NewRetryWrapper(provider, testRetryConfig(), 1, "llama-3.3-70b-versatile", "meta-llama/llama-4-scout-17b-16e-instruct", nil)
			_, err := wrapper.Chat(context.Background(), &ChatRequest{
				Model:    "llama-3.3-70b-versatile",
				Messages: []Message{{Role: RoleUser, Content: "hello"}},
			})

			require.Error(t, err)
			assert.Equal(t, tt.wantCalls, calls.Load())

			var httpErr *ProviderHTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tt.status, httpErr.StatusCode)
			assert.Contains(t, httpErr.Message, tt.wantMessage)
		})
	}
}

func TestGroqVisionHTTPErrorIsTypedAndFinal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"The model llama-9 does not exist","code":"model_not_found"}}`)
	}))
	defer server.Close()

	provider := NewGroqProvider("test-key", false)
	provider.baseURL = server.URL

	wrapper := NewRetryWrapper(provider, testRetryConfig(), 1, "llama-3.3-70b-versatile", "llama-9", nil)
	_, err := wrapper.Vision(context.Background(), &VisionRequest{
		Instruction: "describe this image",
		ImageURL:    "https://example.com/image.png",
	})

	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())

	var httpErr *ProviderHTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	assert.Contains(t, httpErr.Message, "does not exist")
}
