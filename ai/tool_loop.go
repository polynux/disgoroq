package ai

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

func (s *Service) chatWithTools(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if s.tools == nil || !s.tools.Enabled() {
		return s.provider.Chat(ctx, req)
	}

	if req.ToolChoice != nil && strings.EqualFold(req.ToolChoice.Mode, ToolChoiceNone) {
		return s.provider.Chat(ctx, req)
	}

	definitions := mergeToolDefinitions(req.Tools, s.tools.Definitions())
	if len(definitions) == 0 {
		return s.provider.Chat(ctx, req)
	}

	working := cloneChatRequest(req)
	working.Tools = definitions
	if working.ToolChoice == nil {
		working.ToolChoice = &ToolChoice{Mode: ToolChoiceAuto}
	}

	totalCalls := 0
	for round := 0; ; round++ {
		response, err := s.provider.Chat(ctx, working)
		if err != nil {
			return nil, err
		}
		if !response.HasToolCalls() {
			return response, nil
		}
		if round >= s.config.ToolConfig.MaxRounds {
			return nil, fmt.Errorf("tool loop exceeded max rounds (%d)", s.config.ToolConfig.MaxRounds)
		}
		if len(response.ToolCalls) > s.config.ToolConfig.MaxCallsPerRound {
			return nil, fmt.Errorf("tool loop exceeded max calls per round (%d > %d)", len(response.ToolCalls), s.config.ToolConfig.MaxCallsPerRound)
		}

		toolCalls := normalizeToolCalls(response.ToolCalls, round)
		totalCalls += len(toolCalls)
		if totalCalls > s.config.ToolConfig.MaxCallsTotal {
			return nil, fmt.Errorf("tool loop exceeded max total calls (%d > %d)", totalCalls, s.config.ToolConfig.MaxCallsTotal)
		}

		logger.Debug("Executing model tool calls",
			zap.Int("round", round+1),
			zap.Int("tool_call_count", len(toolCalls)),
			zap.String("provider", response.Provider),
			zap.String("model", response.Model))

		working.Messages = append(working.Messages, AssistantToolCallMessage(response.Content, toolCalls...))
		for _, call := range toolCalls {
			result, err := s.tools.Execute(ctx, call)
			if result == nil {
				result = &ToolResult{
					ToolCallID: call.ID,
					ToolName:   call.Function.Name,
					Content:    "tool execution failed",
					IsError:    true,
				}
			}
			if err != nil {
				logger.Warn("Continuing after tool execution error",
					zap.String("tool", call.Function.Name),
					zap.String("tool_call_id", call.ID),
					zap.Error(err))
			}
			working.Messages = append(working.Messages, result.Message())
		}
	}
}

func cloneChatRequest(req *ChatRequest) *ChatRequest {
	if req == nil {
		return nil
	}

	cloned := *req
	cloned.Messages = cloneMessages(req.Messages)
	if len(req.Tools) > 0 {
		cloned.Tools = append([]ToolDefinition(nil), req.Tools...)
	}
	if req.ToolChoice != nil {
		choice := *req.ToolChoice
		cloned.ToolChoice = &choice
	}

	return &cloned
}

func mergeToolDefinitions(base, extra []ToolDefinition) []ToolDefinition {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}

	merged := make([]ToolDefinition, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, definition := range append(append([]ToolDefinition(nil), base...), extra...) {
		name := definition.Function.Name
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, definition)
	}

	return merged
}

func normalizeToolCalls(calls []ToolCall, round int) []ToolCall {
	if len(calls) == 0 {
		return nil
	}

	normalized := make([]ToolCall, len(calls))
	copy(normalized, calls)
	for idx := range normalized {
		if normalized[idx].Type == "" {
			normalized[idx].Type = ToolTypeFunction
		}
		if normalized[idx].ID == "" {
			normalized[idx].ID = fmt.Sprintf("toolcall-r%d-%d", round+1, idx+1)
		}
	}

	return normalized
}
