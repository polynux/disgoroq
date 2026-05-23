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

	"go.uber.org/zap"

	"github.com/ollama/ollama/api"

	"polynux/disgoroq/logger"
)

type OllamaProvider struct {
	client          *api.Client
	baseURL         *url.URL
	httpClient      *http.Client
	thinkingEnabled bool
}

type ollamaChatChunk struct {
	Model   string `json:"model"`
	Error   string `json:"error,omitempty"`
	Message struct {
		Role      string         `json:"role"`
		Content   string         `json:"content"`
		Thinking  string         `json:"thinking,omitempty"`
		ToolCalls []api.ToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	DoneReason      string `json:"done_reason,omitempty"`
	Done            bool   `json:"done"`
	EvalCount       int    `json:"eval_count,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
}

func NewOllamaProvider(baseURL string, thinkingEnabled bool) (*OllamaProvider, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("error creating Ollama client: %w", err)
	}
	httpClient := &http.Client{}
	client := api.NewClient(parsedURL, httpClient)
	return &OllamaProvider{
		client:          client,
		baseURL:         parsedURL,
		httpClient:      httpClient,
		thinkingEnabled: thinkingEnabled,
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

	response, err := o.runChat(ctx, req.Model, messages, buildOllamaTools(req.Tools), req.MaxTokens, req.Temperature)
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
	}, nil, req.MaxTokens, req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("error in Ollama vision chat: %w", err)
	}

	return &VisionResponse{
		Description:      response.Content,
		Model:            response.Model,
		TokensUsed:       response.TokensUsed,
		PromptTokens:     response.PromptTokens,
		CompletionTokens: response.CompletionTokens,
		FinishReason:     response.FinishReason,
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
		if msg.HasToolCalls() {
			ollamaMessage.ToolCalls = buildOllamaToolCalls(msg.ToolCalls)
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

func (o *OllamaProvider) runChat(ctx context.Context, model string, messages []api.Message, tools []api.Tool, maxTokens int, temperature float32) (*ChatResponse, error) {
	chatReq := struct {
		Model    string         `json:"model"`
		Messages []api.Message  `json:"messages"`
		Tools    []api.Tool     `json:"tools,omitempty"`
		Stream   *bool          `json:"stream,omitempty"`
		Think    bool           `json:"think"`
		Options  map[string]any `json:"options,omitempty"`
	}{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   new(bool),
		Think:    o.thinkingEnabled,
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
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	chunks, err := parseOllamaChatResponse(responseBody)
	if err != nil {
		return nil, err
	}

	var responseBuilder strings.Builder
	var thinkingBuilder strings.Builder
	responseModel := model
	finishReason := "stop"
	var tokensUsed int
	var promptTokens int
	var completionTokens int
	var responseToolCalls []ToolCall

	for _, chunk := range chunks {
		if chunk.Error != "" {
			return nil, errors.New(chunk.Error)
		}
		if chunk.Message.Content != "" {
			responseBuilder.WriteString(chunk.Message.Content)
		}
		if chunk.Message.Thinking != "" {
			thinkingBuilder.WriteString(chunk.Message.Thinking)
		}
		if len(chunk.Message.ToolCalls) > 0 {
			responseToolCalls = append(responseToolCalls, parseOllamaToolCalls(chunk.Message.ToolCalls)...)
		}
		if chunk.Model != "" {
			responseModel = chunk.Model
		}
		if chunk.DoneReason != "" {
			finishReason = chunk.DoneReason
		}
		completionTokens = chunk.EvalCount
		promptTokens = chunk.PromptEvalCount
		tokensUsed = chunk.EvalCount + chunk.PromptEvalCount
	}

	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("ollama chat failed: %s", httpResp.Status)
	}

	content := responseBuilder.String()
	thinking := thinkingBuilder.String()
	if content == "" && thinking != "" {
		logger.Warn("Ollama response used thinking field without final content",
			zap.String("model", responseModel),
			zap.Bool("thinking_enabled", o.thinkingEnabled),
			zap.Int("thinking_length", len(thinking)),
			zap.Int("response_chunks", len(chunks)))
		content = thinking
	}

	if content == "" {
		logger.Warn("Ollama response body had no content",
			zap.String("model", responseModel),
			zap.Bool("thinking_enabled", o.thinkingEnabled),
			zap.Int("thinking_length", len(thinking)),
			zap.Int("body_bytes", len(responseBody)),
			zap.Int("response_chunks", len(chunks)))
	}

	return &ChatResponse{
		Content:          content,
		ToolCalls:        responseToolCalls,
		Model:            responseModel,
		TokensUsed:       tokensUsed,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		FinishReason:     finishReason,
	}, nil
}

func buildOllamaTools(definitions []ToolDefinition) []api.Tool {
	if len(definitions) == 0 {
		return nil
	}

	tools := make([]api.Tool, 0, len(definitions))
	for _, definition := range definitions {
		tool := api.Tool{Type: definition.EffectiveType()}
		tool.Function.Name = definition.Function.Name
		tool.Function.Description = definition.Function.Description
		tool.Function.Parameters.Type = definition.Function.Parameters.Type
		tool.Function.Parameters.Required = append([]string(nil), definition.Function.Parameters.Required...)
		tool.Function.Parameters.Properties = make(map[string]struct {
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Enum        []string `json:"enum,omitempty"`
		}, len(definition.Function.Parameters.Properties))
		for name, property := range definition.Function.Parameters.Properties {
			tool.Function.Parameters.Properties[name] = struct {
				Type        string   `json:"type"`
				Description string   `json:"description"`
				Enum        []string `json:"enum,omitempty"`
			}{
				Type:        property.Type,
				Description: property.Description,
				Enum:        append([]string(nil), property.Enum...),
			}
		}
		tools = append(tools, tool)
	}

	return tools
}

func buildOllamaToolCalls(calls []ToolCall) []api.ToolCall {
	if len(calls) == 0 {
		return nil
	}

	result := make([]api.ToolCall, 0, len(calls))
	for _, call := range calls {
		toolCall := api.ToolCall{}
		toolCall.Function.Name = call.Function.Name
		if call.Function.Arguments != "" {
			var arguments map[string]any
			if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err == nil {
				toolCall.Function.Arguments = arguments
			}
		}
		result = append(result, toolCall)
	}

	return result
}

func parseOllamaToolCalls(calls []api.ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}

	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		arguments := "{}"
		if len(call.Function.Arguments) > 0 {
			if encoded, err := json.Marshal(call.Function.Arguments); err == nil {
				arguments = string(encoded)
			}
		}
		result = append(result, ToolCall{
			Type: ToolTypeFunction,
			Function: ToolFunctionCall{
				Name:      call.Function.Name,
				Arguments: arguments,
			},
		})
	}

	return result
}

func parseOllamaChatResponse(body []byte) ([]ollamaChatChunk, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("ollama chat returned empty body")
	}

	var single ollamaChatChunk
	if err := json.Unmarshal(trimmed, &single); err == nil {
		return []ollamaChatChunk{single}, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	chunks := make([]ollamaChatChunk, 0, 8)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var chunk ollamaChatChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			return nil, fmt.Errorf("unmarshal Ollama chat response: %w", err)
		}
		chunks = append(chunks, chunk)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("ollama chat response contained no JSON chunks")
	}

	return chunks, nil
}

func (o *OllamaProvider) downloadImage(ctx context.Context, imageURL string) (api.ImageData, error) {
	if strings.HasPrefix(imageURL, "data:") {
		_, data, err := decodeDataURI(imageURL)
		if err != nil {
			return nil, err
		}
		return api.ImageData(data), nil
	}

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
