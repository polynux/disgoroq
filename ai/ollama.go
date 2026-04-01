package ai

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ollama/ollama/api"
)

type OllamaProvider struct {
	client *api.Client
}

func NewOllamaProvider(baseURL string) (*OllamaProvider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("error creating Ollama client: %w", err)
	}
	client := api.NewClient(parsedURL, nil)
	return &OllamaProvider{
		client: client,
	}, nil
}

func (o *OllamaProvider) Name() string {
	return "ollama"
}

func (o *OllamaProvider) AvailableModels() []ModelInfo {
	return []ModelInfo{
		{Name: "dolphin3", Provider: "ollama", Capabilities: []string{CapabilityChat}},
	}
}

func (o *OllamaProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	messages := make([]api.Message, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, api.Message{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		messages = append(messages, api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	chatReq := &api.ChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   new(bool),
		Options: map[string]any{
			"temperature":   req.Temperature,
			"num_predict":   req.MaxTokens,
			"repeat_last_n": -1,
			"top_k":         60,
		},
	}

	var response string
	var tokensUsed int
	err := o.client.Chat(ctx, chatReq, func(resp api.ChatResponse) error {
		response = resp.Message.Content
		tokensUsed = resp.EvalCount + resp.PromptEvalCount
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error in Ollama chat: %w", err)
	}

	return &ChatResponse{
		Content:      response,
		Model:        req.Model,
		TokensUsed:   tokensUsed,
		FinishReason: "stop",
	}, nil
}

func (o *OllamaProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	return nil, fmt.Errorf("vision not supported by Ollama provider")
}
