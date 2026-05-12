package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scriptedProvider struct {
	responses []*ChatResponse
	requests  []*ChatRequest
}

func (p *scriptedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	p.requests = append(p.requests, cloneChatRequest(req))
	if len(p.responses) == 0 {
		return nil, errors.New("unexpected chat call")
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}

func (p *scriptedProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	return nil, errors.New("not implemented")
}

func (p *scriptedProvider) Name() string {
	return "scripted"
}

func (p *scriptedProvider) AvailableModels() []ModelInfo {
	return nil
}

type stubTool struct {
	definition ToolDefinition
	result     ToolResult
	calls      []ToolCall
}

func (t *stubTool) Definition() ToolDefinition {
	return t.definition
}

func (t *stubTool) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	t.calls = append(t.calls, call)
	result := t.result
	result.ToolCallID = call.ID
	result.ToolName = call.Function.Name
	return &result, nil
}

func TestServiceChatExecutesToolLoop(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*ChatResponse{
			{
				FinishReason: FinishReasonToolCalls,
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Type: ToolTypeFunction,
					Function: ToolFunctionCall{
						Name:      defaultWebToolName,
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
			{
				Content:      "final answer",
				FinishReason: FinishReasonStop,
			},
		},
	}

	tool := &stubTool{
		definition: ToolDefinition{
			Function: ToolFunctionDefinition{
				Name:        defaultWebToolName,
				Description: "fetch web pages",
				Parameters: ToolSchema{
					Type: "object",
					Properties: map[string]ToolProperty{
						"url": {Type: "string"},
					},
					Required: []string{"url"},
				},
			},
		},
		result: ToolResult{Content: "web result"},
	}

	registry := &ToolRegistry{
		config: ToolRuntimeConfig{
			Enabled:          true,
			MaxRounds:        2,
			MaxCallsPerRound: 1,
			MaxCallsTotal:    2,
			Timeout:          time.Second,
		},
		tools: make(map[string]Tool),
	}
	registry.Register(tool)

	service := &Service{
		config: ServiceConfig{
			ToolConfig: ToolRuntimeConfig{
				Enabled:          true,
				MaxRounds:        2,
				MaxCallsPerRound: 1,
				MaxCallsTotal:    2,
				Timeout:          time.Second,
			},
		},
		provider: provider,
		tools:    registry,
	}

	response, err := service.Chat(context.Background(), &ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "hello",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "final answer", response.Content)
	require.Len(t, tool.calls, 1)
	require.Len(t, provider.requests, 2)
	assert.Contains(t, provider.requests[0].SystemPrompt, "Tool-use policy:")
	require.Len(t, provider.requests[1].Messages, 3)
	assert.Equal(t, RoleAssistant, provider.requests[1].Messages[1].Role)
	assert.True(t, provider.requests[1].Messages[1].HasToolCalls())
	assert.Equal(t, RoleTool, provider.requests[1].Messages[2].Role)
	assert.Equal(t, "web result", provider.requests[1].Messages[2].Content)
}

func TestServiceChatStopsWhenToolRoundsExceeded(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*ChatResponse{
			{
				FinishReason: FinishReasonToolCalls,
				ToolCalls: []ToolCall{{
					Function: ToolFunctionCall{
						Name:      defaultWebToolName,
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
			{
				FinishReason: FinishReasonToolCalls,
				ToolCalls: []ToolCall{{
					Function: ToolFunctionCall{
						Name:      defaultWebToolName,
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
		},
	}

	tool := &stubTool{
		definition: ToolDefinition{
			Function: ToolFunctionDefinition{
				Name: defaultWebToolName,
				Parameters: ToolSchema{
					Type: "object",
					Properties: map[string]ToolProperty{
						"url": {Type: "string"},
					},
				},
			},
		},
		result: ToolResult{Content: "web result"},
	}

	registry := &ToolRegistry{
		config: ToolRuntimeConfig{
			Enabled:          true,
			MaxRounds:        1,
			MaxCallsPerRound: 1,
			MaxCallsTotal:    1,
			Timeout:          time.Second,
		},
		tools: make(map[string]Tool),
	}
	registry.Register(tool)

	service := &Service{
		config: ServiceConfig{
			ToolConfig: ToolRuntimeConfig{
				Enabled:          true,
				MaxRounds:        1,
				MaxCallsPerRound: 1,
				MaxCallsTotal:    1,
				Timeout:          time.Second,
			},
		},
		provider: provider,
		tools:    registry,
	}

	_, err := service.Chat(context.Background(), &ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "hello",
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max rounds")
}

func TestServiceChatPrefersWebFetchWhenLatestUserMessageHasURL(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*ChatResponse{{
			Content:      "done",
			FinishReason: FinishReasonStop,
		}},
	}

	tool := &stubTool{
		definition: ToolDefinition{
			Function: ToolFunctionDefinition{
				Name: defaultWebToolName,
				Parameters: ToolSchema{
					Type: "object",
				},
			},
		},
	}

	registry := &ToolRegistry{
		config: ToolRuntimeConfig{
			Enabled:          true,
			MaxRounds:        2,
			MaxCallsPerRound: 1,
			MaxCallsTotal:    2,
			Timeout:          time.Second,
		},
		tools: make(map[string]Tool),
	}
	registry.Register(tool)

	service := &Service{
		config: ServiceConfig{
			ToolConfig: ToolRuntimeConfig{
				Enabled:          true,
				MaxRounds:        2,
				MaxCallsPerRound: 1,
				MaxCallsTotal:    2,
				Timeout:          time.Second,
			},
		},
		provider: provider,
		tools:    registry,
	}

	_, err := service.Chat(context.Background(), &ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "va lire https://example.com et résume-moi la page",
		}},
	})
	require.NoError(t, err)
	require.Len(t, provider.requests, 1)
	require.NotNil(t, provider.requests[0].ToolChoice)
	assert.Equal(t, defaultWebToolName, provider.requests[0].ToolChoice.Name)
	assert.Contains(t, provider.requests[0].SystemPrompt, "call web_fetch")
}

func TestServiceChatRequiresToolForSearchIntent(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*ChatResponse{{
			Content:      "done",
			FinishReason: FinishReasonStop,
		}},
	}

	tool := &stubTool{
		definition: ToolDefinition{
			Function: ToolFunctionDefinition{
				Name: defaultWebSearchToolName,
				Parameters: ToolSchema{
					Type: "object",
				},
			},
		},
	}

	registry := &ToolRegistry{
		config: ToolRuntimeConfig{
			Enabled:          true,
			MaxRounds:        2,
			MaxCallsPerRound: 1,
			MaxCallsTotal:    2,
			Timeout:          time.Second,
		},
		tools: make(map[string]Tool),
	}
	registry.Register(tool)

	service := &Service{
		config: ServiceConfig{
			ToolConfig: ToolRuntimeConfig{
				Enabled:          true,
				MaxRounds:        2,
				MaxCallsPerRound: 1,
				MaxCallsTotal:    2,
				Timeout:          time.Second,
			},
		},
		provider: provider,
		tools:    registry,
	}

	_, err := service.Chat(context.Background(), &ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "cherche sur internet les dernieres infos sur golang",
		}},
	})
	require.NoError(t, err)
	require.Len(t, provider.requests, 1)
	require.NotNil(t, provider.requests[0].ToolChoice)
	assert.Equal(t, ToolChoiceRequired, provider.requests[0].ToolChoice.Mode)
	assert.Contains(t, provider.requests[0].SystemPrompt, "call web_search")
}

func TestServiceChatRequiresToolForFrenchSearchRequestWithoutInternetKeyword(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*ChatResponse{{
			Content:      "done",
			FinishReason: FinishReasonStop,
		}},
	}

	tool := &stubTool{
		definition: ToolDefinition{
			Function: ToolFunctionDefinition{
				Name: defaultWebSearchToolName,
				Parameters: ToolSchema{
					Type: "object",
				},
			},
		},
	}

	registry := &ToolRegistry{
		config: ToolRuntimeConfig{
			Enabled:          true,
			MaxRounds:        2,
			MaxCallsPerRound: 1,
			MaxCallsTotal:    2,
			Timeout:          time.Second,
		},
		tools: make(map[string]Tool),
	}
	registry.Register(tool)

	service := &Service{
		config: ServiceConfig{
			ToolConfig: ToolRuntimeConfig{
				Enabled:          true,
				MaxRounds:        2,
				MaxCallsPerRound: 1,
				MaxCallsTotal:    2,
				Timeout:          time.Second,
			},
		},
		provider: provider,
		tools:    registry,
	}

	_, err := service.Chat(context.Background(), &ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "fais moi une recherche sur le leak de forza horizon 6 stp",
		}},
	})
	require.NoError(t, err)
	require.Len(t, provider.requests, 1)
	require.NotNil(t, provider.requests[0].ToolChoice)
	assert.Equal(t, ToolChoiceRequired, provider.requests[0].ToolChoice.Mode)
}

func TestServiceChatHydratesMissingWebFetchURLFromLatestUserMessage(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*ChatResponse{
			{
				FinishReason: FinishReasonToolCalls,
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Type: ToolTypeFunction,
					Function: ToolFunctionCall{
						Name:      defaultWebToolName,
						Arguments: `{}`,
					},
				}},
			},
			{
				Content:      "done",
				FinishReason: FinishReasonStop,
			},
		},
	}

	tool := &stubTool{
		definition: ToolDefinition{
			Function: ToolFunctionDefinition{
				Name: defaultWebToolName,
				Parameters: ToolSchema{
					Type: "object",
				},
			},
		},
		result: ToolResult{Content: "web result"},
	}

	registry := &ToolRegistry{
		config: ToolRuntimeConfig{
			Enabled:          true,
			MaxRounds:        2,
			MaxCallsPerRound: 1,
			MaxCallsTotal:    2,
			Timeout:          time.Second,
		},
		tools: make(map[string]Tool),
	}
	registry.Register(tool)

	service := &Service{
		config: ServiceConfig{
			ToolConfig: ToolRuntimeConfig{
				Enabled:          true,
				MaxRounds:        2,
				MaxCallsPerRound: 1,
				MaxCallsTotal:    2,
				Timeout:          time.Second,
			},
		},
		provider: provider,
		tools:    registry,
	}

	_, err := service.Chat(context.Background(), &ChatRequest{
		Model: "test-model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "ouvre https://example.com/page et résume",
		}},
	})
	require.NoError(t, err)
	require.Len(t, tool.calls, 1)
	assert.Equal(t, `{"url":"https://example.com/page"}`, tool.calls[0].Function.Arguments)
	require.Len(t, provider.requests, 2)
	require.Len(t, provider.requests[1].Messages, 3)
	assert.Equal(t, `{"url":"https://example.com/page"}`, provider.requests[1].Messages[1].ToolCalls[0].Function.Arguments)
	require.NotNil(t, provider.requests[1].ToolChoice)
	assert.Equal(t, ToolChoiceAuto, provider.requests[1].ToolChoice.Mode)
	assert.Empty(t, provider.requests[1].ToolChoice.Name)
}
