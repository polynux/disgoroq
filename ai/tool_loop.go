package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

var directURLPattern = regexp.MustCompile(`https?://[^\s>]+`)

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
	working.SystemPrompt = buildToolSystemPrompt(working.SystemPrompt, definitions)
	if working.ToolChoice == nil {
		working.ToolChoice = inferToolChoice(working.Messages, definitions)
		if working.ToolChoice != nil {
			logger.Info("Inferred tool choice for chat request",
				zap.String("mode", working.ToolChoice.Mode),
				zap.String("tool", working.ToolChoice.Name))
		}
		if working.ToolChoice == nil {
			working.ToolChoice = &ToolChoice{Mode: ToolChoiceAuto}
		}
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
		toolCalls := normalizeToolCalls(response.ToolCalls, round)
		toolCalls = truncateToolCallsPerRound(toolCalls, s.config.ToolConfig.MaxCallsPerRound)
		toolCalls = truncateToolCallsTotal(toolCalls, s.config.ToolConfig.MaxCallsTotal-totalCalls)
		totalCalls += len(toolCalls)
		if len(toolCalls) == 0 {
			return nil, fmt.Errorf("tool loop reached max total calls (%d)", s.config.ToolConfig.MaxCallsTotal)
		}
		toolCalls = hydrateToolCalls(toolCalls, working.Messages)

		logger.Info("Executing model tool calls",
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
		working.ToolChoice = &ToolChoice{Mode: ToolChoiceAuto}
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

func buildToolSystemPrompt(base string, definitions []ToolDefinition) string {
	if len(definitions) == 0 {
		return base
	}

	hasFetch := hasToolDefinition(definitions, defaultWebToolName)
	hasSearch := hasToolDefinition(definitions, defaultWebSearchToolName)
	if !hasFetch && !hasSearch {
		return base
	}

	var guidance strings.Builder
	if base != "" {
		guidance.WriteString(strings.TrimSpace(base))
		guidance.WriteString("\n\n")
	}
	guidance.WriteString("Tool-use policy:\n")
	guidance.WriteString("- Use available tools whenever the user asks for current web information or asks you to inspect a URL.\n")
	if hasFetch {
		guidance.WriteString("- If the user provides an http/https URL or asks you to open/read/fetch a page, call web_fetch instead of guessing.\n")
	}
	if hasSearch {
		guidance.WriteString("- If the user asks you to search the web, browse online, or find current information without giving a URL, call web_search first.\n")
	}
	guidance.WriteString("- Do not pretend you fetched or searched anything unless you actually used the tool.\n")
	guidance.WriteString("- If a tool result already contains the needed information, answer from that result instead of calling the same tool again with the same input.\n")
	guidance.WriteString("- After tool results are available, answer normally and keep the answer grounded in the fetched/search results.\n")

	return guidance.String()
}

func inferToolChoice(messages []Message, definitions []ToolDefinition) *ToolChoice {
	latestIdx := latestUserMessageIndex(messages)
	if latestIdx < 0 {
		return nil
	}
	latestUser := &messages[latestIdx]

	if hasToolDefinition(definitions, defaultWebToolName) && directURLPattern.MatchString(latestUser.Content) {
		return &ToolChoice{Name: defaultWebToolName}
	}

	if hasToolDefinition(definitions, defaultWebSearchToolName) && looksLikeSearchIntent(latestUser.Content) {
		return &ToolChoice{Mode: ToolChoiceRequired}
	}

	if looksLikeRetryIntent(latestUser.Content) {
		previousUser := previousUserMessageBefore(messages, latestIdx)
		if previousUser != nil {
			if hasToolDefinition(definitions, defaultWebToolName) && directURLPattern.MatchString(previousUser.Content) {
				return &ToolChoice{Name: defaultWebToolName}
			}
			if hasToolDefinition(definitions, defaultWebSearchToolName) && looksLikeSearchIntent(previousUser.Content) {
				return &ToolChoice{Mode: ToolChoiceRequired}
			}
		}
	}

	return nil
}

func latestUserMessage(messages []Message) *Message {
	idx := latestUserMessageIndex(messages)
	if idx < 0 {
		return nil
	}
	return &messages[idx]
}

func latestUserMessageIndex(messages []Message) int {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx].Role == RoleUser && strings.TrimSpace(messages[idx].Content) != "" {
			return idx
		}
	}
	return -1
}

func previousUserMessageBefore(messages []Message, before int) *Message {
	for idx := before - 1; idx >= 0; idx-- {
		if messages[idx].Role == RoleUser && strings.TrimSpace(messages[idx].Content) != "" {
			return &messages[idx]
		}
	}
	return nil
}

func hasToolDefinition(definitions []ToolDefinition, name string) bool {
	for _, definition := range definitions {
		if definition.Function.Name == name {
			return true
		}
	}
	return false
}

func looksLikeSearchIntent(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	if content == "" {
		return false
	}

	explicitPhrases := []string{
		"search the web",
		"search on the web",
		"search internet",
		"search for",
		"web search",
		"browse the web",
		"look up online",
		"look up",
		"go search on internet",
		"find information on",
		"cherche sur internet",
		"cherche sur le web",
		"cherche des infos",
		"cherche des informations",
		"cherche moi",
		"cherche-moi",
		"fais moi une recherche",
		"fais-moi une recherche",
		"fais une recherche",
		"recherche sur",
		"recherche sur internet",
		"trouve des infos",
		"trouve des informations",
		"va chercher sur internet",
		"va voir sur internet",
	}
	for _, phrase := range explicitPhrases {
		if strings.Contains(content, phrase) {
			return true
		}
	}

	searchVerbs := []string{"search", "look up", "lookup", "browse", "cherche", "recherche", "trouve", "va voir", "va chercher"}
	webTargets := []string{"internet", "web", "online", "en ligne"}
	if containsAny(content, searchVerbs) && containsAny(content, webTargets) {
		return true
	}

	currentInfoMarkers := []string{
		"latest news",
		"current info",
		"up-to-date",
		"actualités",
		"actu du jour",
		"quoi de neuf",
		"en ce moment",
		"il se passe quoi",
		"qu'est-ce qui se passe",
		"qu’est-ce qui se passe",
		"what's happening",
		"what is happening",
		"what's going on",
		"what is going on",
	}
	if containsAny(content, currentInfoMarkers) && (containsAny(content, webTargets) || containsAny(content, []string{"news", "actualité", "actu"})) {
		return true
	}

	return false
}

func containsAny(content string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(content, needle) {
			return true
		}
	}
	return false
}

func truncateToolCallsPerRound(calls []ToolCall, maxCalls int) []ToolCall {
	if len(calls) <= maxCalls {
		return calls
	}

	logger.Warn("Model requested too many tool calls in one round; truncating",
		zap.Int("requested_tool_calls", len(calls)),
		zap.Int("max_calls_per_round", maxCalls))
	return calls[:maxCalls]
}

func truncateToolCallsTotal(calls []ToolCall, remaining int) []ToolCall {
	if remaining < 0 {
		remaining = 0
	}
	if len(calls) <= remaining {
		return calls
	}

	logger.Warn("Tool loop reached remaining total call budget; truncating",
		zap.Int("requested_tool_calls", len(calls)),
		zap.Int("remaining_tool_calls", remaining))
	return calls[:remaining]
}

func looksLikeRetryIntent(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	if content == "" {
		return false
	}

	retryPhrases := []string{
		"reessaie",
		"réessaie",
		"reessaye",
		"réessaye",
		"essaie encore",
		"retente",
		"relance la recherche",
		"retry",
		"try again",
		"retry the search",
	}
	return containsAny(content, retryPhrases)
}

func hydrateToolCalls(calls []ToolCall, messages []Message) []ToolCall {
	if len(calls) == 0 {
		return nil
	}

	hydrated := make([]ToolCall, len(calls))
	for idx, call := range calls {
		hydrated[idx] = hydrateToolCallArguments(call, messages)
	}
	return hydrated
}

func hydrateToolCallArguments(call ToolCall, messages []Message) ToolCall {
	if call.Function.Name != defaultWebToolName {
		return call
	}
	if hasNonEmptyURLArgument(call.Function.Arguments) {
		return call
	}

	latestUser := latestUserMessage(messages)
	if latestUser == nil {
		return call
	}

	urls := directURLPattern.FindAllString(latestUser.Content, -1)
	if len(urls) != 1 {
		return call
	}

	payload, err := json.Marshal(struct {
		URL string `json:"url"`
	}{
		URL: urls[0],
	})
	if err != nil {
		return call
	}

	call.Function.Arguments = string(payload)
	logger.Info("Hydrated missing web_fetch URL from latest user message",
		zap.String("tool_call_id", call.ID),
		zap.String("url", urls[0]))
	return call
}

func hasNonEmptyURLArgument(arguments string) bool {
	if strings.TrimSpace(arguments) == "" {
		return false
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return false
	}

	return strings.TrimSpace(payload.URL) != ""
}
