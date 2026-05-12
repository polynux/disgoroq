package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const groqBaseURL = "https://api.groq.com/openai/v1"

type GroqProvider struct {
	apiKey          string
	baseURL         string
	httpClient      *http.Client
	thinkingEnabled bool
}

func NewGroqProvider(apiKey string, thinkingEnabled bool) *GroqProvider {
	return &GroqProvider{
		apiKey:          apiKey,
		baseURL:         groqBaseURL,
		httpClient:      &http.Client{},
		thinkingEnabled: thinkingEnabled,
	}
}

func (g *GroqProvider) Name() string {
	return "groq"
}

func (g *GroqProvider) AvailableModels() []ModelInfo {
	return []ModelInfo{
		{Name: "openai/gpt-oss-20b", Provider: "groq", Capabilities: []string{CapabilityChat}},
		{Name: "llama-3-70b-versatile", Provider: "groq", Capabilities: []string{CapabilityChat}},
		{Name: "meta-llama/llama-4-scout-17b-16e-instruct", Provider: "groq", Capabilities: []string{CapabilityChat, CapabilityVision}},
	}
}

func (g *GroqProvider) SupportsInlineImages(chatModel, visionModel string) bool {
	return chatModel == visionModel && chatModel == "meta-llama/llama-4-scout-17b-16e-instruct"
}

func (g *GroqProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	response, err := g.doChatCompletion(ctx, buildGroqChatRequest(req, g.thinkingEnabled))
	if err != nil {
		return nil, fmt.Errorf("error creating Groq completion: %w", err)
	}

	return &ChatResponse{
		Content:          response.Choices[0].Message.Content,
		ToolCalls:        parseOpenAIToolCalls(response.Choices[0].Message.ToolCalls),
		Model:            req.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		FinishReason:     response.Choices[0].FinishReason,
	}, nil
}

func (g *GroqProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	response, err := g.doChatCompletion(ctx, groqChatCompletionRequest{
		Model: req.Model,
		Messages: []openAIChatMessage{
			{
				Role: RoleUser,
				Content: []openAIMessageContentPart{
					{
						Type: "text",
						Text: req.Instruction,
					},
					{
						Type: "image_url",
						ImageURL: &openAIMessageImageURL{
							URL:    req.ImageURL,
							Detail: "auto",
						},
					},
				},
			},
		},
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: groqReasoningEffort(req.Model, g.thinkingEnabled),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating Groq vision completion: %w", err)
	}

	return &VisionResponse{
		Description:      response.Choices[0].Message.Content,
		Model:            req.Model,
		TokensUsed:       response.Usage.TotalTokens,
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		CachedTokens:     response.Usage.PromptTokensDetails.CachedTokens,
		FinishReason:     response.Choices[0].FinishReason,
	}, nil
}

func buildGroqMessages(req *ChatRequest) []openAIChatMessage {
	return buildOpenAIChatMessages(req, "auto")
}

func buildGroqChatRequest(req *ChatRequest, thinkingEnabled bool) groqChatCompletionRequest {
	return groqChatCompletionRequest{
		Model:           req.Model,
		Messages:        buildGroqMessages(req),
		Tools:           buildOpenAITools(req.Tools),
		ToolChoice:      buildOpenAIToolChoice(req.ToolChoice),
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: groqReasoningEffort(req.Model, thinkingEnabled),
	}
}

func groqReasoningEffort(model string, thinkingEnabled bool) string {
	if thinkingEnabled {
		return ""
	}

	normalizedModel := strings.ToLower(model)
	switch {
	case strings.Contains(normalizedModel, "qwen"):
		return "none"
	case strings.Contains(normalizedModel, "gpt-oss"):
		return "low"
	default:
		return ""
	}
}

func (g *GroqProvider) doChatCompletion(ctx context.Context, req groqChatCompletionRequest) (*groqChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("error marshaling Groq request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("error creating Groq request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("error sending Groq request: %w", err)
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading Groq response: %w", err)
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("Groq request failed: %s: %s", httpResp.Status, string(responseBody))
	}

	var response groqChatCompletionResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error decoding Groq response: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("Groq response did not contain any choices")
	}

	return &response, nil
}

type groqChatCompletionRequest struct {
	Model           string              `json:"model"`
	Messages        []openAIChatMessage `json:"messages"`
	Tools           []openAITool        `json:"tools,omitempty"`
	ToolChoice      any                 `json:"tool_choice,omitempty"`
	MaxTokens       int                 `json:"max_tokens,omitempty"`
	Temperature     float32             `json:"temperature,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
}

type groqChatCompletionResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}
