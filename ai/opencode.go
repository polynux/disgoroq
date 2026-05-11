package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type OpencodeProvider struct {
	apiKey          string
	baseURL         *url.URL
	httpClient      *http.Client
	thinkingEnabled bool
}

func NewOpencodeProvider(baseURL, apiKey string, thinkingEnabled bool) (*OpencodeProvider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("error creating OpenCode client: %w", err)
	}

	return &OpencodeProvider{
		apiKey:          apiKey,
		baseURL:         parsedURL,
		httpClient:      &http.Client{},
		thinkingEnabled: thinkingEnabled,
	}, nil
}

func (o *OpencodeProvider) Name() string {
	return "opencode"
}

func (o *OpencodeProvider) AvailableModels() []ModelInfo {
	return []ModelInfo{
		{Name: "glm-5.1", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "glm-5", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "kimi-k2.5", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "kimi-k2.6", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "deepseek-v4-pro", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "deepseek-v4-flash", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "mimo-v2.5", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "mimo-v2.5-pro", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "qwen3.6-plus", Provider: "opencode", Capabilities: []string{CapabilityChat}},
		{Name: "qwen3.5-plus", Provider: "opencode", Capabilities: []string{CapabilityChat}},
	}
}

func (o *OpencodeProvider) SupportsInlineImages(chatModel, visionModel string) bool {
	return false
}

func (o *OpencodeProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(opencodeChatRequest{
		Model:           req.Model,
		Messages:        buildOpencodeMessages(req),
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: opencodeReasoningEffort(o.thinkingEnabled),
		Stream:          false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling OpenCode chat request: %w", err)
	}

	responseBody, err := o.doChatRequest(ctx, body)
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

	return &ChatResponse{
		Content:          response.Choices[0].Message.Content,
		Model:            response.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: response.Usage.PromptTokensDetails.CacheWriteTokens,
		FinishReason:     response.Choices[0].FinishReason,
	}, nil
}

func (o *OpencodeProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	body, err := json.Marshal(opencodeChatRequest{
		Model: req.Model,
		Messages: []opencodeChatMessage{
			{
				Role: "user",
				Content: []opencodeMessageContentPart{
					{
						Type: "text",
						Text: req.Instruction,
					},
					{
						Type: "image_url",
						ImageURL: &opencodeMessageImageURL{
							URL: req.ImageURL,
						},
					},
				},
			},
		},
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: opencodeReasoningEffort(o.thinkingEnabled),
		Stream:          false,
	})
	if err != nil {
		return nil, fmt.Errorf("error marshaling OpenCode vision request: %w", err)
	}

	responseBody, err := o.doChatRequest(ctx, body)
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

func (o *OpencodeProvider) doChatRequest(ctx context.Context, body []byte) ([]byte, error) {
	requestURL := o.baseURL.JoinPath("chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("error creating OpenCode request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

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
		return nil, fmt.Errorf("OpenCode request failed: %s: %s", httpResp.Status, string(responseBody))
	}

	return responseBody, nil
}

func buildOpencodeMessages(req *ChatRequest) []opencodeChatMessage {
	messages := make([]opencodeChatMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, opencodeChatMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		images := resolveImageRefs(req.Images, msg.ImageRefs)
		if len(images) > 0 {
			parts := make([]opencodeMessageContentPart, 0, len(images)+1)
			if msg.Content != "" {
				parts = append(parts, opencodeMessageContentPart{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, image := range images {
				parts = append(parts, opencodeMessageContentPart{
					Type: "image_url",
					ImageURL: &opencodeMessageImageURL{
						URL: image.URL,
					},
				})
			}
			messages = append(messages, opencodeChatMessage{
				Role:    msg.Role,
				Content: parts,
			})
			continue
		}

		messages = append(messages, opencodeChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	return messages
}

type opencodeChatRequest struct {
	Model           string                `json:"model"`
	Messages        []opencodeChatMessage `json:"messages"`
	MaxTokens       int                   `json:"max_tokens,omitempty"`
	Temperature     float32               `json:"temperature,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
	Stream          bool                  `json:"stream"`
}

type opencodeChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type opencodeMessageContentPart struct {
	Type     string                   `json:"type"`
	Text     string                   `json:"text,omitempty"`
	ImageURL *opencodeMessageImageURL `json:"image_url,omitempty"`
}

type opencodeMessageImageURL struct {
	URL string `json:"url"`
}

type opencodeChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
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

func opencodeReasoningEffort(thinkingEnabled bool) string {
	if thinkingEnabled {
		return ""
	}
	return "none"
}
