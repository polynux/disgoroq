package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ollama/ollama/api"
)

type OllamaProvider struct {
	client     *api.Client
	baseURL    *url.URL
	httpClient *http.Client
}

func NewOllamaProvider(baseURL string) (*OllamaProvider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("error creating Ollama client: %w", err)
	}
	httpClient := &http.Client{}
	client := api.NewClient(parsedURL, httpClient)
	return &OllamaProvider{
		client:     client,
		baseURL:    parsedURL,
		httpClient: httpClient,
	}, nil
}

func (o *OllamaProvider) Name() string {
	return "ollama"
}

func (o *OllamaProvider) AvailableModels() []ModelInfo {
	return []ModelInfo{
		{Name: "dolphin3", Provider: "ollama", Capabilities: []string{CapabilityChat}},
		{Name: "llava", Provider: "ollama", Capabilities: []string{CapabilityChat, CapabilityVision}},
	}
}

func (o *OllamaProvider) SupportsInlineImages(chatModel, visionModel string) bool {
	return chatModel == visionModel && chatModel == "llava"
}

func (o *OllamaProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	messages, err := o.buildChatMessages(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("error preparing Ollama chat: %w", err)
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

func (o *OllamaProvider) buildChatMessages(ctx context.Context, req *ChatRequest) ([]api.Message, error) {
	messages := make([]api.Message, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, api.Message{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		ollamaMessage := api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}

		for _, image := range resolveImageRefs(req.Images, msg.ImageRefs) {
			imageData, err := o.downloadImage(ctx, image.URL)
			if err != nil {
				return nil, fmt.Errorf("error downloading image for Ollama chat: %w", err)
			}
			ollamaMessage.Images = append(ollamaMessage.Images, imageData)
		}

		messages = append(messages, ollamaMessage)
	}

	return messages, nil
}

func (o *OllamaProvider) runChat(ctx context.Context, model string, messages []api.Message, maxTokens int, temperature float32) (*ChatResponse, error) {
	chatReq := struct {
		Model    string         `json:"model"`
		Messages []api.Message  `json:"messages"`
		Stream   *bool          `json:"stream,omitempty"`
		Think    bool           `json:"think"`
		Options  map[string]any `json:"options,omitempty"`
	}{
		Model:    model,
		Messages: messages,
		Stream:   new(bool),
		Think:    false,
		Options: map[string]any{
			"temperature":   temperature,
			"repeat_last_n": -1,
			"top_k":         60,
		},
	}
	if maxTokens > 0 {
		chatReq.Options["num_predict"] = maxTokens
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, err
	}

	requestURL := o.baseURL.JoinPath("/api/chat")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/x-ndjson")

	httpResp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var responseBuilder strings.Builder
	responseModel := model
	finishReason := "stop"
	var tokensUsed int

	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Bytes()

		var errorResponse struct {
			Error string `json:"error,omitempty"`
		}
		if err := json.Unmarshal(line, &errorResponse); err != nil {
			return nil, fmt.Errorf("unmarshal Ollama response: %w", err)
		}
		if errorResponse.Error != "" {
			return nil, errors.New(errorResponse.Error)
		}
		if httpResp.StatusCode >= http.StatusBadRequest {
			return nil, fmt.Errorf("ollama chat failed: %s", httpResp.Status)
		}

		var resp api.ChatResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal Ollama chat response: %w", err)
		}

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
	}
	if err := scanner.Err(); err != nil {
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
