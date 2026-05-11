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
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter chat response did not contain any choices")
	}

	return &ChatResponse{
		Content:      response.Choices[0].Message.Content,
		Model:        response.Model,
		TokensUsed:   response.Usage.TotalTokens,
		FinishReason: response.Choices[0].FinishReason,
	}, nil
}

func (o *OpenrouterProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	body, err := json.Marshal(openrouterChatRequest{
		Model: req.Model,
		Messages: []openrouterChatMessage{
			{
				Role: "user",
				Content: []openrouterMessageContentPart{
					{
						Type: "text",
						Text: req.Instruction,
					},
					{
						Type: "image_url",
						ImageURL: &openrouterMessageImageURL{
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
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenRouter vision response did not contain any choices")
	}

	return &VisionResponse{
		Description:  response.Choices[0].Message.Content,
		Model:        response.Model,
		TokensUsed:   response.Usage.TotalTokens,
		FinishReason: response.Choices[0].FinishReason,
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
		return nil, fmt.Errorf("OpenRouter request failed: %s: %s", httpResp.Status, string(responseBody))
	}

	return responseBody, nil
}

func buildOpenrouterMessages(req *ChatRequest) []openrouterChatMessage {
	messages := make([]openrouterChatMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, openrouterChatMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		images := resolveImageRefs(req.Images, msg.ImageRefs)
		if len(images) > 0 {
			parts := make([]openrouterMessageContentPart, 0, len(images)+1)
			if msg.Content != "" {
				parts = append(parts, openrouterMessageContentPart{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, image := range images {
				parts = append(parts, openrouterMessageContentPart{
					Type: "image_url",
					ImageURL: &openrouterMessageImageURL{
						URL: image.URL,
					},
				})
			}
			messages = append(messages, openrouterChatMessage{
				Role:    msg.Role,
				Content: parts,
			})
			continue
		}

		messages = append(messages, openrouterChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	return messages
}

type openrouterChatRequest struct {
	Model               string                  `json:"model"`
	Messages            []openrouterChatMessage `json:"messages"`
	MaxCompletionTokens int                     `json:"max_completion_tokens,omitempty"`
	Temperature         float32                 `json:"temperature,omitempty"`
	Reasoning           *openrouterReasoning    `json:"reasoning,omitempty"`
	Stream              bool                    `json:"stream"`
}

type openrouterChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openrouterMessageContentPart struct {
	Type     string                     `json:"type"`
	Text     string                     `json:"text,omitempty"`
	ImageURL *openrouterMessageImageURL `json:"image_url,omitempty"`
}

type openrouterMessageImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type openrouterReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Exclude bool   `json:"exclude,omitempty"`
}

type openrouterChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
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
