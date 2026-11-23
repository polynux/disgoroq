package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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
		_, err = io.WriteString(w, `{"model":"google/gemini-2.5-flash","choices":[{"message":{"content":"hi","reasoning":"hidden"},"finish_reason":"stop"}],"usage":{"prompt_tokens":40,"completion_tokens":12,"total_tokens":52,"prompt_tokens_details":{"cached_tokens":32,"cache_write_tokens":8}}}`)
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
	assert.Equal(t, 52, resp.TokensUsed)
	assert.Equal(t, 40, resp.PromptTokens)
	assert.Equal(t, 12, resp.CompletionTokens)
	assert.Equal(t, 32, resp.CachedTokens)
	assert.Equal(t, 8, resp.CacheWriteTokens)
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

		var parts []openAIMessageContentPart
		err = json.Unmarshal(partsData, &parts)
		require.NoError(t, err)

		require.Len(t, parts, 2)
		assert.Equal(t, "text", parts[0].Type)
		assert.Equal(t, "describe", parts[0].Text)
		assert.Equal(t, "image_url", parts[1].Type)
		require.NotNil(t, parts[1].ImageURL)
		assert.Equal(t, "https://example.com/cat.png", parts[1].ImageURL.URL)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"google/gemini-2.5-flash","choices":[{"message":{"content":"cat"},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":8,"total_tokens":26,"prompt_tokens_details":{"cached_tokens":12,"cache_write_tokens":4}}}`)
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
	assert.Equal(t, 26, resp.TokensUsed)
	assert.Equal(t, 18, resp.PromptTokens)
	assert.Equal(t, 8, resp.CompletionTokens)
	assert.Equal(t, 12, resp.CachedTokens)
	assert.Equal(t, 4, resp.CacheWriteTokens)
}

func TestOpenrouterSupportsInlineImagesWhenModelsMatch(t *testing.T) {
	provider, err := NewOpenrouterProvider("https://openrouter.ai/api/v1", "test-key", true)
	require.NoError(t, err)

	assert.True(t, provider.SupportsInlineImages("google/gemini-2.5-flash", "google/gemini-2.5-flash"))
	assert.False(t, provider.SupportsInlineImages("google/gemini-2.5-flash", "openai/gpt-4.1-mini"))
}

func TestOpenrouterChatParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req openrouterChatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		require.Len(t, req.Tools, 1)
		assert.Equal(t, defaultWebToolName, req.Tools[0].Function.Name)
		assert.Equal(t, "auto", req.ToolChoice)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"google/gemini-2.5-flash","choices":[{"message":{"content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpenrouterProvider(server.URL, "test-key", false)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "google/gemini-2.5-flash",
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

func TestOpenrouterHTTPErrorsAreTypedAndClassified(t *testing.T) {
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
			body:        `{"error":{"message":"No endpoints found for model google/does-not-exist.","code":404}}`,
			wantMessage: "No endpoints found",
			wantCalls:   1,
		},
		{
			name:        "429 rate limit is retried",
			status:      http.StatusTooManyRequests,
			body:        `{"error":{"message":"Rate limit exceeded: free-models-per-min.","code":429}}`,
			wantMessage: "Rate limit exceeded",
			wantCalls:   3,
		},
		{
			name:        "429 daily allowance is final",
			status:      http.StatusTooManyRequests,
			body:        `{"error":{"message":"Rate limit exceeded: free-models-per-day.","code":429}}`,
			wantMessage: "free-models-per-day",
			wantCalls:   1,
		},
		{
			name:        "503 is retried",
			status:      http.StatusServiceUnavailable,
			body:        `{"error":{"message":"upstream provider unavailable"}}`,
			wantMessage: "upstream provider unavailable",
			wantCalls:   3,
		},
		{
			name:        "401 is final",
			status:      http.StatusUnauthorized,
			body:        `{"error":{"message":"No auth credentials found","code":401}}`,
			wantMessage: "No auth credentials found",
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

			provider, err := NewOpenrouterProvider(server.URL, "test-key", false)
			require.NoError(t, err)

			wrapper := NewRetryWrapper(provider, testRetryConfig(), 1, "google/gemini-2.5-flash", "google/gemini-2.5-flash", nil)
			_, err = wrapper.Chat(context.Background(), &ChatRequest{
				Model:    "google/gemini-2.5-flash",
				Messages: []Message{{Role: RoleUser, Content: "hello"}},
			})

			require.Error(t, err)
			assert.Equal(t, tt.wantCalls, calls.Load())

			var httpErr *ProviderHTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tt.status, httpErr.StatusCode)
			assert.Contains(t, httpErr.Message, tt.wantMessage)
			assert.Contains(t, err.Error(), "OpenRouter")
		})
	}
}

// OpenRouter can report a failure inside a 200 response; it must be typed with
// the code from the body so the retry rules apply.
func TestOpenrouterErrorInsideOKResponse(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCalls  int32
	}{
		{"402 in the body is final", `{"error":{"code":402,"message":"Provider returned error"}}`, http.StatusPaymentRequired, 1},
		{"502 in the body is retried", `{"error":{"code":502,"message":"Provider returned error"}}`, http.StatusBadGateway, 3},
		{"string code is parsed", `{"error":{"code":"429","message":"Rate limit exceeded: free-models-per-min."}}`, http.StatusTooManyRequests, 3},
		{"missing code is treated as a transient upstream error", `{"error":{"message":"Provider returned error"}}`, http.StatusBadGateway, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			provider, err := NewOpenrouterProvider(server.URL, "test-key", false)
			require.NoError(t, err)

			wrapper := NewRetryWrapper(provider, testRetryConfig(), 1, "google/gemini-2.5-flash", "google/gemini-2.5-flash", nil)
			_, err = wrapper.Chat(context.Background(), &ChatRequest{
				Model:    "google/gemini-2.5-flash",
				Messages: []Message{{Role: RoleUser, Content: "hello"}},
			})

			require.Error(t, err)
			var httpErr *ProviderHTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tt.wantStatus, httpErr.StatusCode)
			assert.Equal(t, tt.wantCalls, calls.Load())
			assert.Contains(t, err.Error(), "OpenRouter")
		})
	}

	t.Run("choices with an error field are still returned", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"error":{"code":502},"choices":[{"message":{"content":"partial"},"finish_reason":"error"}]}`)
		}))
		defer server.Close()

		provider, err := NewOpenrouterProvider(server.URL, "test-key", false)
		require.NoError(t, err)
		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model:    "google/gemini-2.5-flash",
			Messages: []Message{{Role: RoleUser, Content: "hello"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "partial", resp.Content)
	})
}

func TestOpenrouterVisionHTTPErrorIsTypedAndFinal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"No endpoints found for model google/gemini-9.","code":404}}`)
	}))
	defer server.Close()

	provider, err := NewOpenrouterProvider(server.URL, "test-key", false)
	require.NoError(t, err)

	wrapper := NewRetryWrapper(provider, testRetryConfig(), 1, "google/gemini-2.5-flash", "google/gemini-9", nil)
	_, err = wrapper.Vision(context.Background(), &VisionRequest{
		Instruction: "describe this image",
		ImageURL:    "https://example.com/image.png",
	})

	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())

	var httpErr *ProviderHTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	assert.Contains(t, httpErr.Message, "No endpoints found")
}
