package ai

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIMessageContentPart struct {
	Type     string                 `json:"type"`
	Text     string                 `json:"text,omitempty"`
	ImageURL *openAIMessageImageURL `json:"image_url,omitempty"`
}

type openAIMessageImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  ToolSchema `json:"parameters"`
	Strict      bool       `json:"strict,omitempty"`
}

type openAIToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolFunctionCall `json:"function"`
}

func buildOpenAIChatMessages(req *ChatRequest, imageDetail string) []openAIChatMessage {
	messages := make([]openAIChatMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, openAIChatMessage{
			Role:    RoleSystem,
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		if msg.HasToolCalls() {
			messages = append(messages, openAIChatMessage{
				Role:      msg.Role,
				Content:   msg.Content,
				Name:      msg.Name,
				ToolCalls: buildOpenAIToolCalls(msg.ToolCalls),
			})
			continue
		}

		if msg.Role == RoleTool {
			messages = append(messages, openAIChatMessage{
				Role:       RoleTool,
				Content:    msg.Content,
				Name:       msg.Name,
				ToolCallID: msg.ToolCallID,
			})
			continue
		}

		images := resolveImageRefs(req.Images, msg.ImageRefs)
		if msg.Role == RoleUser && len(images) > 0 {
			parts := make([]openAIMessageContentPart, 0, len(images)+1)
			if msg.Content != "" {
				parts = append(parts, openAIMessageContentPart{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, image := range images {
				parts = append(parts, openAIMessageContentPart{
					Type: "image_url",
					ImageURL: &openAIMessageImageURL{
						URL:    image.URL,
						Detail: imageDetail,
					},
				})
			}
			messages = append(messages, openAIChatMessage{
				Role:    msg.Role,
				Content: parts,
				Name:    msg.Name,
			})
			continue
		}

		messages = append(messages, openAIChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
			Name:    msg.Name,
		})
	}

	return messages
}

func buildOpenAITools(definitions []ToolDefinition) []openAITool {
	if len(definitions) == 0 {
		return nil
	}

	tools := make([]openAITool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, openAITool{
			Type: definition.EffectiveType(),
			Function: openAIToolFunction{
				Name:        definition.Function.Name,
				Description: definition.Function.Description,
				Parameters:  definition.Function.Parameters,
				Strict:      definition.Function.Strict,
			},
		})
	}

	return tools
}

func buildOpenAIToolChoice(choice *ToolChoice) any {
	if choice == nil {
		return nil
	}
	if choice.Name != "" {
		return struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}{
			Type: ToolTypeFunction,
			Function: struct {
				Name string `json:"name"`
			}{
				Name: choice.Name,
			},
		}
	}
	if choice.Mode == "" {
		return ToolChoiceAuto
	}
	return choice.Mode
}

func buildOpenAIToolCalls(calls []ToolCall) []openAIToolCall {
	if len(calls) == 0 {
		return nil
	}

	result := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, openAIToolCall{
			ID:       call.ID,
			Type:     call.EffectiveType(),
			Function: call.Function,
		})
	}
	return result
}

func parseOpenAIToolCalls(calls []openAIToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}

	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, ToolCall{
			ID:       call.ID,
			Type:     call.Type,
			Function: call.Function,
		})
	}
	return result
}
