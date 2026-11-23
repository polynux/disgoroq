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
		name          string
		statusCode    int
		contentType   string
		body          string
		wantContains  string
		wantRetryable bool
		wantTruncated bool
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
			name:          "400 invalid_request_error media download failure is not retried",
			statusCode:    http.StatusBadRequest,
			contentType:   "application/json",
			body:          `{"error":{"type":"invalid_request_error","code":"upstream_error","message":"Failed to download image: media_url_origin_error"}}`,
			wantContains:  "invalid_request_error",
			wantRetryable: false,
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

func TestOpencodeSendsUserAgentAndSessionHeaders(t *testing.T) {
	var seenUserAgents []string
	var seenSessions []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUserAgents = append(seenUserAgents, r.Header.Get("User-Agent"))
		seenSessions = append(seenSessions, r.Header.Get("x-opencode-session"))

		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOpencodeProvider(server.URL, "test-key", false)
	require.NoError(t, err)

	// Explicit SessionID is forwarded as x-opencode-session.
	_, err = provider.Chat(context.Background(), &ChatRequest{
		Model:     "deepseek-v4.1-flash",
		SessionID: "guild-1:channel-2",
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
	})
	require.NoError(t, err)

	// Missing SessionID falls back to the provider's stable default session ID.
	_, err = provider.Chat(context.Background(), &ChatRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []Message{{Role: RoleUser, Content: "second"}},
	})
	require.NoError(t, err)

	// Vision requests also send User-Agent and the same stable fallback session ID.
	_, err = provider.Vision(context.Background(), &VisionRequest{
		Model:       "deepseek-v4.1-flash",
		Instruction: "describe",
		ImageURL:    "data:image/png;base64,iVBORw0KGgo=",
	})
	require.NoError(t, err)

	require.Len(t, seenUserAgents, 3)
	for _, ua := range seenUserAgents {
		assert.Equal(t, opencodeUserAgent, ua)
	}

	require.Len(t, seenSessions, 3)
	assert.Equal(t, "guild-1:channel-2", seenSessions[0])
	assert.NotEmpty(t, seenSessions[1])
	assert.Equal(t, seenSessions[1], seenSessions[2], "fallback x-opencode-session should be stable across requests on the same provider instance")
	assert.NotEqual(t, seenSessions[0], seenSessions[1])
}

func TestOpencodeAvailableModelsAndSupportsInlineImages(t *testing.T) {
	provider, err := NewOpencodeProvider("https://opencode.ai/inference/go/v1", "test-key", true)
	require.NoError(t, err)

	modelsByName := make(map[string]ModelInfo)
	for _, model := range provider.AvailableModels() {
		assert.Equal(t, "opencode", model.Provider)
		modelsByName[model.Name] = model
	}

	assert.Contains(t, modelsByName["deepseek-v4.1-flash"].Capabilities, CapabilityVision)
	assert.Contains(t, modelsByName["muse-spark-1.3-contributor"].Capabilities, CapabilityVision)
	assert.Contains(t, modelsByName["kimi-k3"].Capabilities, CapabilityVision)
	assert.Contains(t, modelsByName["minimax-m3"].Capabilities, CapabilityVision)
	assert.NotContains(t, modelsByName["deepseek-v4-pro"].Capabilities, CapabilityVision)
	assert.NotContains(t, modelsByName["glm-5.3"].Capabilities, CapabilityVision)

	assert.True(t, provider.SupportsInlineImages("deepseek-v4.1-flash", "deepseek-v4.1-flash"))
	assert.True(t, provider.SupportsInlineImages("muse-spark-1.3-contributor", "muse-spark-1.3-contributor"))
	assert.True(t, provider.SupportsInlineImages("kimi-k3", "kimi-k3"))
	assert.False(t, provider.SupportsInlineImages("deepseek-v4.1-flash", "muse-spark-1.3-contributor"))
	assert.False(t, provider.SupportsInlineImages("deepseek-v4-pro", "deepseek-v4-pro"))
	assert.False(t, provider.SupportsInlineImages("glm-5.3", "glm-5.3"))
	assert.False(t, provider.SupportsInlineImages("", ""))
	assert.False(t, provider.SupportsInlineImages("unknown-model", "unknown-model"))
}

func TestOpencodeInlineImagesWithRetryWrapper(t *testing.T) {
	const dataURI = "data:image/png;base64,iVBORw0KGgo="

	t.Run("sends images inline in a single request when shared model supports vision", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			var req opencodeChatRequest
			require.NoError(t, json.Unmarshal(body, &req))
			assert.Equal(t, "deepseek-v4.1-flash", req.Model)
			require.Len(t, req.Messages, 1)

			partsData, err := json.Marshal(req.Messages[0].Content)
			require.NoError(t, err)
			var parts []openAIMessageContentPart
			require.NoError(t, json.Unmarshal(partsData, &parts))
			require.Len(t, parts, 2)
			assert.Equal(t, "text", parts[0].Type)
			assert.Equal(t, "what is this?", parts[0].Text)
			assert.Equal(t, "image_url", parts[1].Type)
			require.NotNil(t, parts[1].ImageURL)
			assert.Equal(t, dataURI, parts[1].ImageURL.URL)

			w.Header().Set("Content-Type", "application/json")
			_, err = io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"a tiny png"},"finish_reason":"stop"}]}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)
		wrapper := NewRetryWrapper(provider, DefaultRetryConfig(), 1, "deepseek-v4.1-flash", "deepseek-v4.1-flash", nil)

		resp, err := wrapper.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{
				Role:      RoleUser,
				Content:   "what is this?",
				ImageRefs: []int{0},
			}},
			Images: []ImageContext{{
				URL:  dataURI,
				Type: "image/png",
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, "a tiny png", resp.Content)
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("uses separate vision call when chat model is text-only", func(t *testing.T) {
		var seenModels []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			var req opencodeChatRequest
			require.NoError(t, json.Unmarshal(body, &req))
			seenModels = append(seenModels, req.Model)

			w.Header().Set("Content-Type", "application/json")
			if len(seenModels) == 1 {
				_, err = io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"a red square"},"finish_reason":"stop"}]}`)
			} else {
				_, err = io.WriteString(w, `{"model":"deepseek-v4-pro","choices":[{"message":{"content":"looks red"},"finish_reason":"stop"}]}`)
			}
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)
		wrapper := NewRetryWrapper(provider, DefaultRetryConfig(), 1, "deepseek-v4-pro", "deepseek-v4.1-flash", nil)

		resp, err := wrapper.Chat(context.Background(), &ChatRequest{
			Messages: []Message{{
				Role:      RoleUser,
				Content:   "what is this?",
				ImageRefs: []int{0},
			}},
			Images: []ImageContext{{
				URL:  dataURI,
				Type: "image/png",
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, "looks red", resp.Content)
		assert.Equal(t, []string{"deepseek-v4.1-flash", "deepseek-v4-pro"}, seenModels)
	})
}

func TestOpencodeReasoningEffort(t *testing.T) {
	tests := []struct {
		name            string
		model           string
		thinkingEnabled bool
		wantField       bool
		wantEffort      string
	}{
		{
			name:            "deepseek thinking off sends none",
			model:           "deepseek-v4.1-flash",
			thinkingEnabled: false,
			wantField:       true,
			wantEffort:      "none",
		},
		{
			name:            "muse 1.3 thinking off sends minimal",
			model:           "muse-spark-1.3-contributor",
			thinkingEnabled: false,
			wantField:       true,
			wantEffort:      "minimal",
		},
		{
			name:            "muse 1.2 thinking off sends minimal",
			model:           "muse-spark-1.2-contributor",
			thinkingEnabled: false,
			wantField:       true,
			wantEffort:      "minimal",
		},
		{
			name:            "deepseek thinking on omits reasoning_effort",
			model:           "deepseek-v4.1-flash",
			thinkingEnabled: true,
			wantField:       false,
		},
		{
			name:            "muse thinking on omits reasoning_effort",
			model:           "muse-spark-1.3-contributor",
			thinkingEnabled: true,
			wantField:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var chatRaw map[string]any
			var visionRaw map[string]any
			var callCount int

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var raw map[string]any
				require.NoError(t, json.Unmarshal(body, &raw))
				callCount++
				if callCount == 1 {
					chatRaw = raw
				} else {
					visionRaw = raw
				}

				w.Header().Set("Content-Type", "application/json")
				_, err = io.WriteString(w, `{"model":"`+tc.model+`","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
				require.NoError(t, err)
			}))
			defer server.Close()

			provider, err := NewOpencodeProvider(server.URL, "test-key", tc.thinkingEnabled)
			require.NoError(t, err)

			_, err = provider.Chat(context.Background(), &ChatRequest{
				Model:    tc.model,
				Messages: []Message{{Role: RoleUser, Content: "hi"}},
			})
			require.NoError(t, err)

			_, err = provider.Vision(context.Background(), &VisionRequest{
				Model:       tc.model,
				Instruction: "describe",
				ImageURL:    "data:image/png;base64,iVBORw0KGgo=",
			})
			require.NoError(t, err)

			for _, payload := range []map[string]any{chatRaw, visionRaw} {
				val, exists := payload["reasoning_effort"]
				assert.Equal(t, tc.wantField, exists)
				if tc.wantField {
					assert.Equal(t, tc.wantEffort, val)
				}
			}
		})
	}
}

func TestOpencodeReasoningResponses(t *testing.T) {
	t.Run("empty content with reasoning_content succeeds without leaking reasoning", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"","reasoning_content":"step 1: think about reply"},"finish_reason":"length"}],"usage":{"prompt_tokens":20,"completion_tokens":200,"total_tokens":220}}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model:    "deepseek-v4.1-flash",
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Content)
		assert.Equal(t, "length", resp.FinishReason)
		assert.Equal(t, 220, resp.TokensUsed)
	})

	t.Run("null content with reasoning_content succeeds without crashing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":null,"reasoning_content":"internal thoughts"},"finish_reason":"length"}]}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model:    "deepseek-v4.1-flash",
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Content)
		assert.Equal(t, "length", resp.FinishReason)
	})

	t.Run("content alongside reasoning_content returns only final content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"final reply","reasoning_content":"internal thoughts"},"finish_reason":"stop"}]}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model:    "deepseek-v4.1-flash",
			Messages: []Message{{Role: RoleUser, Content: "hi"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "final reply", resp.Content)
		assert.Equal(t, "stop", resp.FinishReason)
	})

	t.Run("empty content with reasoning_content and tool_calls parses tool calls", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":null,"reasoning_content":"I should fetch that URL","tool_calls":[{"id":"call-1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}]}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model:    "deepseek-v4.1-flash",
			Messages: []Message{{Role: RoleUser, Content: "check https://example.com"}},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Content)
		require.Len(t, resp.ToolCalls, 1)
		assert.Equal(t, "web_fetch", resp.ToolCalls[0].Function.Name)
		assert.Equal(t, FinishReasonToolCalls, resp.FinishReason)
	})

	t.Run("vision empty content with reasoning_content succeeds without crashing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"model":"deepseek-v4.1-flash","choices":[{"message":{"content":"","reasoning_content":"looking at image"},"finish_reason":"length"}]}`)
			require.NoError(t, err)
		}))
		defer server.Close()

		provider, err := NewOpencodeProvider(server.URL, "test-key", true)
		require.NoError(t, err)

		resp, err := provider.Vision(context.Background(), &VisionRequest{
			Model:       "deepseek-v4.1-flash",
			Instruction: "describe",
			ImageURL:    "data:image/png;base64,iVBORw0KGgo=",
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Description)
		assert.Equal(t, "length", resp.FinishReason)
	})
}

func TestOpencodeReasoningEffortTable(t *testing.T) {
	tests := []struct {
		model    string
		thinking bool
		want     string
	}{
		{"deepseek-v4.1-flash", false, "none"},
		{"deepseek-v4-pro", false, "none"},
		{"kimi-k3", false, "none"},
		{"glm-5.3", false, "none"},
		{"minimax-m3", false, "none"},
		{"muse-spark-1.3-contributor", false, "minimal"},
		{"opencode-go/muse-spark-1.3-contributor", false, "minimal"},
		{"OpenCode-Go/DeepSeek-V4.1-Flash", false, "none"},
		// Rejects "none" live; unknown models keep their default too.
		{"kimi-k2.7-code", false, ""},
		{"some-new-model", false, ""},
		{"deepseek-v4.1-flash", true, ""},
		{"muse-spark-1.3-contributor", true, ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, opencodeReasoningEffort(tt.model, tt.thinking), "model %q thinking %v", tt.model, tt.thinking)
	}
}

func TestStripLeadingThinkBlock(t *testing.T) {
	tests := map[string]string{
		"Bonjour !": "Bonjour !",
		"<think>We need answer French</think>Bonjour !": "Bonjour !",
		"\n <think>a\nb</think>\n\nBonjour !":           "Bonjour !",
		"<think>never closed":                           "",
		"Bonjour <think>not leading</think>":            "Bonjour <think>not leading</think>",
		"":                                              "",
	}
	for in, want := range tests {
		assert.Equal(t, want, stripLeadingThinkBlock(in), "input %q", in)
	}
}
