package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolDefinitionEffectiveTypeDefaultsToFunction(t *testing.T) {
	assert.Equal(t, ToolTypeFunction, ToolDefinition{}.EffectiveType())
	assert.Equal(t, ToolTypeFunction, ToolCall{}.EffectiveType())
}

func TestAssistantToolCallMessage(t *testing.T) {
	msg := AssistantToolCallMessage("checking", ToolCall{
		ID: "call-1",
		Function: ToolFunctionCall{
			Name:      "web_fetch",
			Arguments: `{"url":"https://example.com"}`,
		},
	})

	assert.Equal(t, RoleAssistant, msg.Role)
	assert.Equal(t, "checking", msg.Content)
	assert.True(t, msg.HasToolCalls())
	assert.Len(t, msg.ToolCalls, 1)
	assert.Equal(t, "web_fetch", msg.ToolCalls[0].Function.Name)
}

func TestToolResultMessage(t *testing.T) {
	msg := ToolResultMessage("call-1", "web_fetch", "page content")

	assert.Equal(t, RoleTool, msg.Role)
	assert.Equal(t, "call-1", msg.ToolCallID)
	assert.Equal(t, "web_fetch", msg.Name)
	assert.Equal(t, "page content", msg.Content)
	assert.False(t, msg.HasToolCalls())
}

func TestChatResponseHasToolCalls(t *testing.T) {
	assert.False(t, (*ChatResponse)(nil).HasToolCalls())
	assert.False(t, (&ChatResponse{}).HasToolCalls())
	assert.True(t, (&ChatResponse{
		ToolCalls: []ToolCall{{ID: "call-1"}},
	}).HasToolCalls())
}
