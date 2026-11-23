package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestOpencodeChatParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req opencodeChatRequest
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		require.Len(t, req.Tools, 1)
		assert.Equal(t, defaultWebToolName, req.Tools[0].Function.Name)
		assert.Equal(t, "auto", req.ToolChoice)

		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, `{"model":"deepseek-v4-flash","choices":[{"message":{"content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpencodeProvider(server.URL, "test-key", false)
	require.NoError(t, err)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "deepseek-v4-flash",
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

func TestOpencodeHTTPErrorMapping(t *testing.T) {
	longHTML := "<html><body><h1>502 Bad Gateway</h1><p>" + strings.Repeat("upstream proxy failure ", 25) + "</p></body></html>"

	tests := []struct {
		name            string
		statusCode      int
		contentType     string
		body            string
		wantContains    string
		wantRetryable   bool
		wantTruncated   bool
	}{
		{
			name:          "401 api_key_expired is permanent",
			statusCode:    http.StatusUnauthorized,
			contentType:   "application/json",
			body:          `{"error":{"type":"auth_error","code":"api_key_expired","message":"API key has expired"}}`,
			wantContains:  "api_key_expired",
			wantRetryable: false,
		},
		{
			name:          "403 EntitlementError is permanent",
			statusCode:    http.StatusForbidden,
			contentType:   "application/json",
			body:          `{"error":{"type":"EntitlementError","message":"This API key is not covered by OpenCode Go"}}`,
			wantContains:  "EntitlementError",
			wantRetryable: false,
		},
		{
			name:          "502 non-JSON body is retryable and truncated",
			statusCode:    http.StatusBadGateway,
			contentType:   "text/html",
			body:          longHTML,
			wantContains:  "502 Bad Gateway",
			wantRetryable: true,
			wantTruncated: true,
		},
		{
			name:          "400 invalid_request_error media download failure is not permanent quota",
			statusCode:    http.StatusBadRequest,
			contentType:   "application/json",
			body:          `{"error":{"type":"invalid_request_error","code":"upstream_error","message":"Failed to download image: media_url_origin_error"}}`,
			wantContains:  "invalid_request_error",
			wantRetryable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.statusCode)
				_, err := io.WriteString(w, tc.body)
				require.NoError(t, err)
			}))
			defer server.Close()

			provider, err := NewOpencodeProvider(server.URL, "test-key", false)
			require.NoError(t, err)

			_, err = provider.Chat(context.Background(), &ChatRequest{
				Model:    "deepseek-v4.1-flash",
				Messages: []Message{{Role: RoleUser, Content: "hello"}},
			})
			require.Error(t, err)

			var statusErr *ProviderHTTPError
			require.ErrorAs(t, err, &statusErr)
			assert.Equal(t, tc.statusCode, statusErr.StatusCode)
			assert.Contains(t, statusErr.Message, tc.wantContains)
			assert.Equal(t, tc.wantRetryable, isRetryableProviderError(err))
			if tc.wantTruncated {
				assert.True(t, strings.HasSuffix(statusErr.Message, "…"))
				assert.LessOrEqual(t, len([]rune(statusErr.Message)), 301)
			}
		})
	}
}

func TestOpencodeRetryWrapperPermanentVsTransientErrors(t *testing.T) {
	retryCfg := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  time.Millisecond,
		MaxDelay:      2 * time.Millisecond,
		BackoffFactor: 1.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}

	t.Run("does not retry 403 entitlement error", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, err := io.WriteString(w, `{"error":{"type":"EntitlementError","message":"Not covered by Go"}}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", false)
		require.NoError(t, err)
		wrapper := NewRetryWrapper(provider, retryCfg, 1, "deepseek-v4.1-flash", "deepseek-v4.1-flash", nil)

		_, err = wrapper.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "hello"}},
		})
		require.Error(t, err)
		assert.Equal(t, int32(1), attempts.Load())
	})

	t.Run("retries 502 and succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if attempts.Add(1) == 1 {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusBadGateway)
				_, err := io.WriteString(w, "Bad Gateway")
				require.NoError(t, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"recovered"},"finish_reason":"stop"}]}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", false)
		require.NoError(t, err)
		wrapper := NewRetryWrapper(provider, retryCfg, 1, "deepseek-v4.1-flash", "deepseek-v4.1-flash", nil)

		resp, err := wrapper.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{Role: RoleUser, Content: "hello"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "recovered", resp.Content)
		assert.Equal(t, int32(2), attempts.Load())
	})
}
