package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompletionPrompt(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: regarde ça https://tenor.com/view/x <:kappa:42>\n\n", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleAssistant, Content: "mdrrr", AuthorID: "9", AuthorNick: "Bot"},
		{Role: RoleUser, Content: "<@2>Bob: <@1> t'as\nvu\n\n", AuthorID: "2", AuthorNick: "Bob"},
		{Role: RoleUser, Content: "<@2>Bob: [Document Summary]\nrésumé\n\nx\n\n", AuthorID: "2", AuthorNick: "Bob"},
		{Role: "tool", Content: "ignored"},
	}

	prompt := buildCompletionPrompt(messages, "Groq")

	assert.Equal(t, "Alice: regarde ça [gif] :kappa:\nGroq: mdrrr\nBob: @Alice t'as vu\nGroq:", prompt)
}

func TestBuildCompletionPromptKeepsMostRecentLinesWithinBudget(t *testing.T) {
	var messages []Message
	for i := 0; i < 50; i++ {
		messages = append(messages, Message{Role: RoleUser, Content: strings.Repeat("a", 100), AuthorID: "1", AuthorNick: "Alice"})
	}
	messages = append(messages, Message{Role: RoleUser, Content: "dernier", AuthorID: "1", AuthorNick: "Alice"})

	prompt := buildCompletionPrompt(messages, "")

	assert.LessOrEqual(t, len(prompt), completionPromptChars+len("\nbot:"))
	assert.True(t, strings.HasSuffix(prompt, "Alice: dernier\nbot:"))
}

func TestCleanCompletionReply(t *testing.T) {
	assert.Equal(t, "ptdr t'es grave", cleanCompletionReply(" @Yu ptdr t'es grave [gif]"))
	assert.Equal(t, "", cleanCompletionReply(" @Deleted"))
}

func TestOllamaCompletionModeUsesRawGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/generate", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "discord-v2", payload["model"])
		assert.Equal(t, "Alice: yo\nGroq:", payload["prompt"])
		assert.Equal(t, true, payload["raw"])
		assert.NotContains(t, payload, "tools")
		options := payload["options"].(map[string]any)
		assert.Equal(t, []any{"\n"}, options["stop"])
		assert.EqualValues(t, completionMaxTokens, options["num_predict"])

		_, err = io.WriteString(w, `{"model":"discord-v2","response":" wesh @Bob","done":true,"done_reason":"stop","eval_count":3,"prompt_eval_count":5}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL, false)
	require.NoError(t, err)
	provider.SetCompletionMode(true)

	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model:        "discord-v2",
		SystemPrompt: "ignored instructions",
		Messages:     []Message{{Role: RoleUser, Content: "<@1>Alice: yo\n\n", AuthorID: "1", AuthorNick: "Alice"}},
		Tools:        []ToolDefinition{{}},
		MaxTokens:    200,
		BotName:      "Groq",
	})
	require.NoError(t, err)
	assert.Equal(t, "wesh", resp.Content)
	assert.Equal(t, 8, resp.TokensUsed)
}
