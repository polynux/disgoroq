package ai

import (
	"context"
	"fmt"

	"github.com/conneroisu/groq-go"
)

type GroqProvider struct {
	apiKey string
}

func NewGroqProvider(apiKey string) *GroqProvider {
	return &GroqProvider{
		apiKey: apiKey,
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
	client, err := groq.NewClient(g.apiKey)
	if err != nil {
		return nil, fmt.Errorf("error creating Groq client: %w", err)
	}

	resp, err := client.ChatCompletion(ctx, groq.ChatCompletionRequest{
		Model:       groq.ChatModel(req.Model),
		Messages:    buildGroqMessages(req),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating Groq completion: %w", err)
	}

	return &ChatResponse{
		Content:      string(resp.Choices[0].Message.Content),
		Model:        req.Model,
		TokensUsed:   resp.Usage.TotalTokens,
		FinishReason: string(resp.Choices[0].FinishReason),
	}, nil
}

func (g *GroqProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	client, err := groq.NewClient(g.apiKey)
	if err != nil {
		return nil, fmt.Errorf("error creating Groq client: %w", err)
	}

	resp, err := client.ChatCompletion(ctx, groq.ChatCompletionRequest{
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
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating Groq vision completion: %w", err)
	}

	return &VisionResponse{
		Description:  string(resp.Choices[0].Message.Content),
		Model:        req.Model,
		TokensUsed:   resp.Usage.TotalTokens,
		FinishReason: string(resp.Choices[0].FinishReason),
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
