package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
	client := api.NewClient(parsedURL, &http.Client{})
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
		{Name: "llava", Provider: "ollama", Capabilities: []string{CapabilityVision}},
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

	response, err := o.runChat(ctx, req.Model, messages, req.MaxTokens, req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("error in Ollama chat: %w", err)
	}

	return response, nil
}

func (o *OllamaProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	imageData, err := o.downloadImage(ctx, req.ImageURL)
	if err != nil {
		return nil, fmt.Errorf("error downloading image for Ollama vision: %w", err)
	}

	response, err := o.runChat(ctx, req.Model, []api.Message{
		{
			Role:    "user",
			Content: req.Instruction,
			Images:  []api.ImageData{imageData},
		},
	}, req.MaxTokens, req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("error in Ollama vision chat: %w", err)
	}

	return &VisionResponse{
		Description:  response.Content,
		Model:        response.Model,
		TokensUsed:   response.TokensUsed,
		FinishReason: response.FinishReason,
	}, nil
}

func (o *OllamaProvider) runChat(ctx context.Context, model string, messages []api.Message, maxTokens int, temperature float32) (*ChatResponse, error) {
	chatReq := &api.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   new(bool),
		Options: map[string]any{
			"temperature":   temperature,
			"repeat_last_n": -1,
			"top_k":         60,
		},
	}
	if maxTokens > 0 {
		chatReq.Options["num_predict"] = maxTokens
	}

	var responseBuilder strings.Builder
	responseModel := model
	finishReason := "stop"
	var tokensUsed int
	err := o.client.Chat(ctx, chatReq, func(resp api.ChatResponse) error {
		if resp.Message.Content != "" {
			responseBuilder.WriteString(resp.Message.Content)
		}
		if resp.Model != "" {
			responseModel = resp.Model
		}
		if resp.DoneReason != "" {
			finishReason = resp.DoneReason
		}
		tokensUsed = resp.EvalCount + resp.PromptEvalCount
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Content:      responseBuilder.String(),
		Model:        responseModel,
		TokensUsed:   tokensUsed,
		FinishReason: finishReason,
	}, nil
}

func (o *OllamaProvider) downloadImage(ctx context.Context, imageURL string) (api.ImageData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("image payload is empty")
	}

	return api.ImageData(data), nil
}
