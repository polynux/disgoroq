package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type OpenrouterProvider struct {
	apiKey          string
	baseURL         *url.URL
	httpClient      *http.Client
	thinkingEnabled bool
}

func NewOpenrouterProvider(baseURL, apiKey string, thinkingEnabled bool) (*OpenrouterProvider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("error creating OpenRouter client: %w", err)
	}

	return &OpenrouterProvider{
		apiKey:          apiKey,
		baseURL:         parsedURL,
		httpClient:      &http.Client{},
		thinkingEnabled: thinkingEnabled,
	}, nil
}

func (o *OpenrouterProvider) Name() string {
	return "openrouter"
}

func (o *OpenrouterProvider) AvailableModels() []ModelInfo {
	return []ModelInfo{
		{Name: "google/gemini-2.5-flash", Provider: "openrouter", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "openai/gpt-4.1-mini", Provider: "openrouter", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "anthropic/claude-3.7-sonnet", Provider: "openrouter", Capabilities: []string{CapabilityChat, CapabilityVision}},
		{Name: "openai/gpt-5.2", Provider: "openrouter", Capabilities: []string{CapabilityChat}},
	}
}

func (o *OpenrouterProvider) SupportsInlineImages(chatModel, visionModel string) bool {
	return chatModel != "" && chatModel == visionModel
}

func (o *OpenrouterProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(openrouterChatRequest{
		Model:               req.Model,
		Messages:            buildOpenrouterMessages(req),
		Tools:               buildOpenAITools(req.Tools),
		ToolChoice:          buildOpenAIToolChoice(req.ToolChoice),
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		Reasoning:           openrouterReasoningConfig(o.thinkingEnabled),
		Stream:              false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling OpenRouter chat request: %w", err)
	}

	responseBody, err := o.doChatRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var response openrouterChatResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error decoding OpenRouter chat response: %w", err)
	}
	if err := response.envelopeError(responseBody); err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter chat response did not contain any choices")
	}

	return &ChatResponse{
		Content:          response.Choices[0].Message.Content,
		ToolCalls:        parseOpenAIToolCalls(response.Choices[0].Message.ToolCalls),
		Model:            response.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: response.Usage.PromptTokensDetails.CacheWriteTokens,
		FinishReason:     response.Choices[0].FinishReason,
	}, nil
}

func (o *OpenrouterProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	body, err := json.Marshal(openrouterChatRequest{
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
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		Reasoning:           openrouterReasoningConfig(o.thinkingEnabled),
		Stream:              false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling OpenRouter vision request: %w", err)
	}

	responseBody, err := o.doChatRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var response openrouterChatResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error decoding OpenRouter vision response: %w", err)
	}
	if err := response.envelopeError(responseBody); err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter vision response did not contain any choices")
	}

	return &VisionResponse{
		Description:      response.Choices[0].Message.Content,
		Model:            response.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: response.Usage.PromptTokensDetails.CacheWriteTokens,
		FinishReason:     response.Choices[0].FinishReason,
	}, nil
}

func (o *OpenrouterProvider) doChatRequest(ctx context.Context, body []byte) ([]byte, error) {
	requestURL := o.baseURL.JoinPath("chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("error creating OpenRouter request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("error sending OpenRouter request: %w", err)
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading OpenRouter response: %w", err)
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("OpenRouter request failed: %w", &ProviderHTTPError{StatusCode: httpResp.StatusCode, Message: errorBodySnippet(responseBody)})
	}

	return responseBody, nil
}

func buildOpenrouterMessages(req *ChatRequest) []openAIChatMessage {
	return buildOpenAIChatMessages(req, "")
}

type openrouterChatRequest struct {
	Model               string               `json:"model"`
	Messages            []openAIChatMessage  `json:"messages"`
	Tools               []openAITool         `json:"tools,omitempty"`
	ToolChoice          any                  `json:"tool_choice,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Temperature         float32              `json:"temperature,omitempty"`
	Reasoning           *openrouterReasoning `json:"reasoning,omitempty"`
	Stream              bool                 `json:"stream"`
}

type openrouterReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Exclude bool   `json:"exclude,omitempty"`
}

type openrouterChatResponse struct {
	Model string `json:"model"`
	// Error is set when OpenRouter reports a failure inside a 200 response,
	// e.g. an upstream provider error after the headers were sent.
	Error *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			Reasoning string           `json:"reasoning,omitempty"`
			ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
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

func openrouterReasoningConfig(thinkingEnabled bool) *openrouterReasoning {
	if thinkingEnabled {
		return nil
	}

	return &openrouterReasoning{
		Effort:  "none",
		Exclude: true,
	}
}

// envelopeError turns an error reported inside a 200 response into a typed
// ProviderHTTPError, using the code OpenRouter puts in the body, so the retry
// rules see the real status. It returns nil when there is no error, or when
// the first choice still carries content or tool calls (a partial reply).
func (r *openrouterChatResponse) envelopeError(body []byte) error {
	if r.Error == nil {
		return nil
	}
	if len(r.Choices) > 0 && (strings.TrimSpace(r.Choices[0].Message.Content) != "" || len(r.Choices[0].Message.ToolCalls) > 0) {
		return nil
	}
	status := http.StatusBadGateway // unknown upstream failure: treat as transient
	if isQuotaMessage(r.Error.Message) {
		status = http.StatusPaymentRequired // no usable code, but the account is out of credits
	}
	switch code := r.Error.Code.(type) {
	case float64:
		if code >= 400 && code < 600 {
			status = int(code)
		}
	case string:
		if parsed, err := strconv.Atoi(code); err == nil && parsed >= 400 && parsed < 600 {
			status = parsed
		}
	}
	return fmt.Errorf("OpenRouter request failed: %w", &ProviderHTTPError{StatusCode: status, Message: errorBodySnippet(body)})
}
