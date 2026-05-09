package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/conneroisu/groq-go"
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
		Content:      string(response.Choices[0].Message.Content),
		Model:        req.Model,
		TokensUsed:   response.Usage.TotalTokens,
		FinishReason: string(response.Choices[0].FinishReason),
	}, nil
}

func (g *GroqProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	response, err := g.doChatCompletion(ctx, groqChatCompletionRequest{
		Model: groq.ChatModel(req.Model),
		Messages: []groq.ChatCompletionMessage{
			{
				Role: groq.RoleUser,
				MultiContent: []groq.ChatMessagePart{
					{
						Type: groq.ChatMessagePartTypeText,
						Text: req.Instruction,
					},
					{
						Type: groq.ChatMessagePartTypeImageURL,
						ImageURL: &groq.ChatMessageImageURL{
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
		Description:  string(response.Choices[0].Message.Content),
		Model:        req.Model,
		TokensUsed:   response.Usage.TotalTokens,
		FinishReason: string(response.Choices[0].FinishReason),
	}, nil
}

func buildGroqMessages(req *ChatRequest) []groq.ChatCompletionMessage {
	groqMessages := make([]groq.ChatCompletionMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		groqMessages = append(groqMessages, groq.ChatCompletionMessage{
			Role:    groq.RoleSystem,
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		images := resolveImageRefs(req.Images, msg.ImageRefs)
		if msg.Role == "user" && len(images) > 0 {
			parts := make([]groq.ChatMessagePart, 0, len(images)+1)
			if msg.Content != "" {
				parts = append(parts, groq.ChatMessagePart{
					Type: groq.ChatMessagePartTypeText,
					Text: msg.Content,
				})
			}
			for _, image := range images {
				parts = append(parts, groq.ChatMessagePart{
					Type: groq.ChatMessagePartTypeImageURL,
					ImageURL: &groq.ChatMessageImageURL{
						URL:    image.URL,
						Detail: "auto",
					},
				})
			}
			groqMessages = append(groqMessages, groq.ChatCompletionMessage{
				Role:         groq.Role(msg.Role),
				MultiContent: parts,
			})
			continue
		}

		groqMessages = append(groqMessages, groq.ChatCompletionMessage{
			Role:    groq.Role(msg.Role),
			Content: msg.Content,
		})
	}

	return groqMessages
}

func buildGroqChatRequest(req *ChatRequest, thinkingEnabled bool) groqChatCompletionRequest {
	return groqChatCompletionRequest{
		Model:           groq.ChatModel(req.Model),
		Messages:        buildGroqMessages(req),
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

func (g *GroqProvider) doChatCompletion(ctx context.Context, req groqChatCompletionRequest) (*groq.ChatCompletionResponse, error) {
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

	var response groq.ChatCompletionResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("error decoding Groq response: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("Groq response did not contain any choices")
	}

	return &response, nil
}

type groqChatCompletionRequest struct {
	Model           groq.ChatModel               `json:"model"`
	Messages        []groq.ChatCompletionMessage `json:"messages"`
	MaxTokens       int                          `json:"max_tokens,omitempty"`
	Temperature     float32                      `json:"temperature,omitempty"`
	ReasoningEffort string                       `json:"reasoning_effort,omitempty"`
}
