package ai

import (
	"testing"

	"github.com/conneroisu/groq-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroqBuildMessagesInlinesReferencedImages(t *testing.T) {
	req := &ChatRequest{
		SystemPrompt: "system prompt",
		Messages: []Message{
			{
				Role:      "user",
				Content:   "describe this",
				ImageRefs: []int{0, 1},
			},
			{
				Role:    "assistant",
				Content: "done",
			},
		},
		Images: []ImageContext{
			{URL: "https://example.com/one.png"},
			{URL: "https://example.com/two.png"},
		},
	}

	messages := buildGroqMessages(req)

	require.Len(t, messages, 3)
	assert.Equal(t, groq.RoleSystem, messages[0].Role)
	assert.Equal(t, "system prompt", messages[0].Content)

	assert.Equal(t, groq.RoleUser, messages[1].Role)
	require.Len(t, messages[1].MultiContent, 3)
	assert.Equal(t, groq.ChatMessagePartTypeText, messages[1].MultiContent[0].Type)
	assert.Equal(t, "describe this", messages[1].MultiContent[0].Text)
	assert.Equal(t, groq.ChatMessagePartTypeImageURL, messages[1].MultiContent[1].Type)
	assert.Equal(t, "https://example.com/one.png", messages[1].MultiContent[1].ImageURL.URL)
	assert.Equal(t, groq.ChatMessagePartTypeImageURL, messages[1].MultiContent[2].Type)
	assert.Equal(t, "https://example.com/two.png", messages[1].MultiContent[2].ImageURL.URL)

	assert.Equal(t, groq.RoleAssistant, messages[2].Role)
	assert.Equal(t, "done", messages[2].Content)
}

func TestGroqSupportsInlineImagesOnlyForSharedScoutModel(t *testing.T) {
	provider := NewGroqProvider("test-key")

	assert.True(t, provider.SupportsInlineImages(
		"meta-llama/llama-4-scout-17b-16e-instruct",
		"meta-llama/llama-4-scout-17b-16e-instruct",
	))
	assert.False(t, provider.SupportsInlineImages(
		"openai/gpt-oss-20b",
		"meta-llama/llama-4-scout-17b-16e-instruct",
	))
	assert.False(t, provider.SupportsInlineImages(
		"meta-llama/llama-4-scout-17b-16e-instruct",
		"openai/gpt-oss-20b",
	))
}
