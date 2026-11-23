package ai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

const (
	opencodeUserAgent       = "disgoroq/1.0"
	opencodeFallbackSession = "disgoroq-default"
)

type OpencodeProvider struct {
	apiKey           string
	baseURL          *url.URL
	httpClient       *http.Client
	thinkingEnabled  bool
	defaultSessionID string
}

func NewOpencodeProvider(baseURL, apiKey string, thinkingEnabled bool) (*OpencodeProvider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("error creating OpenCode client: %w", err)
	}

	return &OpencodeProvider{
		apiKey:           apiKey,
		baseURL:          parsedURL,
		httpClient:       &http.Client{},
		thinkingEnabled:  thinkingEnabled,
		defaultSessionID: newOpencodeSessionID(),
	}, nil
}

func newOpencodeSessionID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return opencodeFallbackSession
	}
	return "disgoroq-" + hex.EncodeToString(raw[:])
}

func (o *OpencodeProvider) sessionID(requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	if o.defaultSessionID != "" {
		return o.defaultSessionID
	}
	return opencodeFallbackSession
}

func (o *OpencodeProvider) Name() string {
	return "opencode"
}

func (o *OpencodeProvider) AvailableModels() []ModelInfo {
	return []ModelInfo{
		{Name: "deepseek-v4.1-flash", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "deepseek-v4-pro", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "muse-spark-1.3-contributor", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "muse-spark-1.2-contributor", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "kimi-k3", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "kimi-k3.1-pro", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "kimi-k2.7-code", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "minimax-m3", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "minimax-m3.1-flash", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "glm-5.3", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "glm-5.3-flash", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "mimo-v2.6-pro", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "mimo-v2.6-flash", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "mimo-v2.5", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "mimo-v2.5-pro", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "qwen4-max", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "qwen3.8-max", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "qwen3.8-flash", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "qwen3.7-plus", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "claude-haiku-5-5", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "gemini-4-argon", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "gpt-6-luna", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "gpt-5.6-luna", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "grok-4.7", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "longcat-2.5", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "step-5-preview", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "hy4", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "hy4-preview", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "hy3", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "hy4-flash-free", Provider: "opencode", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "iceman-contributor-free", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "beam-free", Provider: "opencode", Capabilities: []string{CapabilityChat}},
	}
}

func (o *OpencodeProvider) SupportsInlineImages(chatModel, visionModel string) bool {
	if chatModel == "" || chatModel != visionModel {
		return false
	}
	for _, model := range o.AvailableModels() {
		if model.Name != chatModel {
			continue
		}
		for _, capability := range model.Capabilities {
			if capability == CapabilityVision {
				return true
			}
		}
		return false
	}
	return false
}

func (o *OpencodeProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(opencodeChatRequest{
		Model:           req.Model,
		Messages:        buildOpencodeMessages(req),
		Tools:           buildOpenAITools(req.Tools),
		ToolChoice:      buildOpenAIToolChoice(req.ToolChoice),
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: opencodeReasoningEffort(req.Model, o.thinkingEnabled),
		Stream:          false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling OpenCode chat request: %w", err)
	}

	responseBody, err := o.doChatRequest(ctx, body, req.SessionID)
	if err != nil {
		return nil, err
	}

	var response opencodeChatResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error decoding OpenCode chat response: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenCode chat response did not contain any choices")
	}

	choice := response.Choices[0]
	choice.Message.Content = stripLeadingThinkBlock(choice.Message.Content)
	toolCalls := parseOpenAIToolCalls(choice.Message.ToolCalls)
	if choice.Message.Content == "" && len(toolCalls) == 0 && choice.Message.ReasoningContent != "" {
		model := response.Model
		if model == "" {
			model = req.Model
		}
		logger.Warn("OpenCode chat response returned reasoning_content without final content",
			zap.String("model", model),
			zap.Bool("thinking_enabled", o.thinkingEnabled),
			zap.Int("reasoning_length", len(choice.Message.ReasoningContent)),
			zap.String("finish_reason", choice.FinishReason))
	}

	return &ChatResponse{
		Content:          choice.Message.Content,
		ToolCalls:        toolCalls,
		Model:            response.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: response.Usage.PromptTokensDetails.CacheWriteTokens,
		FinishReason:     choice.FinishReason,
	}, nil
}

func (o *OpencodeProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	body, err := json.Marshal(opencodeChatRequest{
		Model: req.Model,
		Messages: []openAIChatMessage{
			{
				Role: "user",
				Content: []openAIMessageContentPart{
					{
						Type: "text",
						Text: req.Instruction,
					},
					{
						Type: "image_url",
						ImageURL: &openAIMessageImageURL{
							URL: req.ImageURL,
						},
					},
				},
			},
		},
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: opencodeReasoningEffort(req.Model, o.thinkingEnabled),
		Stream:          false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling OpenCode vision request: %w", err)
	}

	responseBody, err := o.doChatRequest(ctx, body, req.SessionID)
	if err != nil {
		return nil, err
	}

	var response opencodeChatResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error decoding OpenCode vision response: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenCode vision response did not contain any choices")
	}

	choice := response.Choices[0]
	choice.Message.Content = stripLeadingThinkBlock(choice.Message.Content)
	if choice.Message.Content == "" && choice.Message.ReasoningContent != "" {
		model := response.Model
		if model == "" {
			model = req.Model
		}
		logger.Warn("OpenCode vision response returned reasoning_content without final content",
			zap.String("model", model),
			zap.Bool("thinking_enabled", o.thinkingEnabled),
			zap.Int("reasoning_length", len(choice.Message.ReasoningContent)),
			zap.String("finish_reason", choice.FinishReason))
	}

	return &VisionResponse{
		Description:      choice.Message.Content,
		Model:            response.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: response.Usage.PromptTokensDetails.CacheWriteTokens,
		FinishReason:     choice.FinishReason,
	}, nil
}

func (o *OpencodeProvider) doChatRequest(ctx context.Context, body []byte, sessionID string) ([]byte, error) {
	requestURL := o.baseURL.JoinPath("chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("error creating OpenCode request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", opencodeUserAgent)
	httpReq.Header.Set("x-opencode-session", o.sessionID(sessionID))

	httpResp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("error sending OpenCode request: %w", err)
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading OpenCode response: %w", err)
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, &ProviderHTTPError{
			StatusCode: httpResp.StatusCode,
			Message:    errorBodySnippet(responseBody),
		}
	}

	return responseBody, nil
}

func buildOpencodeMessages(req *ChatRequest) []openAIChatMessage {
	return buildOpenAIChatMessages(req, "")
}

type opencodeChatRequest struct {
	Model           string              `json:"model"`
	Messages        []openAIChatMessage `json:"messages"`
	Tools           []openAITool        `json:"tools,omitempty"`
	ToolChoice      any                 `json:"tool_choice,omitempty"`
	MaxTokens       int                 `json:"max_tokens,omitempty"`
	Temperature     float32             `json:"temperature,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
	Stream          bool                `json:"stream"`
}

type opencodeChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content,omitempty"`
			ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// opencodeNoReasoningPrefixes are the model families checked live (2026-11-23)
// to accept reasoning_effort "none" on /chat/completions. Other models either
// reject "none" (kimi-k2.7-code: "only type=enabled is allowed") or were not
// checked, so they get no reasoning_effort field and keep their default.
var opencodeNoReasoningPrefixes = []string{"deepseek-", "kimi-k3", "glm-5.3", "minimax-m3"}

// opencodeReasoningEffort picks the reasoning_effort to send when thinking is
// off. An empty result omits the field.
func opencodeReasoningEffort(model string, thinkingEnabled bool) string {
	if thinkingEnabled {
		return ""
	}
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	// Accept provider-qualified names such as "opencode-go/muse-spark-1.3-contributor".
	if i := strings.LastIndex(normalizedModel, "/"); i >= 0 {
		normalizedModel = normalizedModel[i+1:]
	}
	if strings.HasPrefix(normalizedModel, "muse-") {
		// Muse rejects "none"; "minimal" is its lowest accepted value.
		return "minimal"
	}
	for _, prefix := range opencodeNoReasoningPrefixes {
		if strings.HasPrefix(normalizedModel, prefix) {
			return "none"
		}
	}
	return ""
}

// stripLeadingThinkBlock removes a leading <think>...</think> block. Some models
// (minimax-m3) put their reasoning inline in content instead of
// reasoning_content, and it must not reach users.
func stripLeadingThinkBlock(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "<think>") {
		return content
	}
	end := strings.Index(trimmed, "</think>")
	if end < 0 {
		// Unterminated reasoning: nothing in it is a reply.
		return ""
	}
	return strings.TrimLeft(trimmed[end+len("</think>"):], " \t\r\n")
}
