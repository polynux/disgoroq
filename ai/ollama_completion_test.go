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

func TestBuildCompletionPromptLucioleFormatAndTargetLast(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: quelqu'un a vu le match hier ?", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleAssistant, Content: "ouais trop nul", AuthorNick: "bot"},
		{Role: RoleUser, Content: "<@2>Bob: c’était chaud", AuthorID: "2", AuthorNick: "Bob"},
		{Role: RoleUser, Content: "<@3>Léa: t'es d'accord ?", AuthorID: "3", AuthorNick: "Léa", MessageID: "target"},
	}
	got := buildCompletionPrompt(messages, "DisgoroQ", "target", "§bot", 2400)
	want := "Alice: quelqu'un a vu le match hier ?\n§bot: ouais trop nul\nBob: c'était chaud\nLéa: t'es d'accord ?\n§bot:"
	assert.Equal(t, want, got)
}

func TestBuildCompletionPromptMovesTargetAfterNewerMessages(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: cible", AuthorID: "1", AuthorNick: "Alice", MessageID: "target"},
		{Role: RoleUser, Content: "<@2>Bob: plus récent", AuthorID: "2", AuthorNick: "Bob", MessageID: "newer"},
	}
	assert.Equal(t, "Bob: plus récent\nAlice: cible\nbot:", buildCompletionPrompt(messages, "bot", "target", "", 2400))
}

func TestBuildCompletionPromptKeepsTwoMostRecentBotLines(t *testing.T) {
	var messages []Message
	for i := 1; i <= 3; i++ {
		messages = append(messages, Message{Role: RoleAssistant, Content: "bot line", AuthorNick: "bot", MessageID: string(rune('0' + i))})
		messages = append(messages, Message{Role: RoleUser, Content: "Alice: human line", AuthorNick: "Alice"})
	}
	prompt := buildCompletionPrompt(messages, "Display", "", "§bot", 2400)
	assert.Equal(t, 2, strings.Count(prompt, "§bot: bot line"))
	assert.Equal(t, 3, strings.Count(prompt, "Alice: human line"))
}

func TestBuildCompletionPromptLegacySpeakerAndCleaning(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: regarde ça https://tenor.com/view/x <:kappa:42>\n\n", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleAssistant, Content: "mdrrr", AuthorID: "9", AuthorNick: "Bot"},
		{Role: RoleUser, Content: "<@2>Bob: <@1> t'as\nvu\n\n", AuthorID: "2", AuthorNick: "Bob"},
		{Role: RoleUser, Content: "<@2>Bob: [Document Summary]\nrésumé\n\n", AuthorID: "2", AuthorNick: "Bob"},
		{Role: "tool", Content: "ignored"},
	}
	assert.Equal(t, "Alice: regarde ça [gif] :kappa:\nBot: mdrrr\nBob: @Alice t'as vu\nBot:", buildCompletionPrompt(messages, "Bot", "", "", 2400))
}

func TestBuildCompletionPromptBudgetKeepsLatestAndTarget(t *testing.T) {
	var messages []Message
	for i := 0; i < 50; i++ {
		messages = append(messages, Message{Role: RoleUser, Content: "<@1>Alice: " + strings.Repeat("a", 100), AuthorID: "1", AuthorNick: "Alice"})
	}
	messages = append(messages, Message{Role: RoleUser, Content: "<@2>Bob: target", AuthorID: "2", AuthorNick: "Bob", MessageID: "target"})
	prompt := buildCompletionPrompt(messages, "bot", "target", "", 2400)
	assert.LessOrEqual(t, len(prompt), 2400+len("\nbot:"))
	assert.True(t, strings.HasSuffix(prompt, "Bob: target\nbot:"))
	assert.Contains(t, prompt, strings.Repeat("a", 100))

	targetOnly := buildCompletionPrompt(messages, "bot", "target", "", 10)
	assert.True(t, strings.Contains(targetOnly, "Bob: target\nbot:"))
}

func TestBuildCompletionPromptExcludesBotsCommandsAndErrorReplies(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: human", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleUser, Content: "<@2>OtherBot: bot chatter", AuthorID: "2", AuthorNick: "OtherBot", AuthorBot: true},
		{Role: RoleAssistant, Content: "mémoire désactivée", AuthorNick: "Bot", CommandOutput: true},
		{Role: RoleAssistant, Content: "Désolé, tous mes systèmes sont en rade... 🤖💀", AuthorNick: "Bot"},
	}
	prompt := buildCompletionPrompt(messages, "Bot", "", "§bot", 2400)
	assert.Equal(t, "Alice: human\n§bot:", prompt)
}

func TestCleanCompletionReply(t *testing.T) {
	assert.Equal(t, "salut Bot mdr: x", cleanCompletionReply("Alice: salut §bot mdr: x", "Bot"))
	assert.Equal(t, "ptdr t'es grave", cleanCompletionReply(" @Yu ptdr t'es grave [gif]", "Bot"))
	assert.Equal(t, "", cleanCompletionReply(" @Deleted", "Bot"))
	// Raw replies start with a space because the prompt ends with "§bot:".
	assert.Equal(t, "ouais grave", cleanCompletionReply(" Bob: ouais grave", "Bot"))
	assert.Equal(t, "ok Bot", cleanCompletionReply(" §bot: ok §bot", "Bot"))
}

func TestOllamaCompletionModeUsesRawGenerateAndRetriesEmpty(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/api/generate", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "luciole-1b-discord1", payload["model"])
		assert.Equal(t, "Alice: yo\n§bot:", payload["prompt"])
		assert.Equal(t, true, payload["raw"])
		assert.NotContains(t, payload, "tools")
		options := payload["options"].(map[string]any)
		assert.NotContains(t, options, "temperature")
		assert.Equal(t, []any{"\n"}, options["stop"])
		assert.EqualValues(t, completionMaxTokens, options["num_predict"])
		_, err = io.WriteString(w, `{"model":"luciole-1b-discord1","response":" @Deleted","done":true}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL, false)
	require.NoError(t, err)
	provider.SetCompletionMode(true)
	resp, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "luciole-1b-discord1", Messages: []Message{{Role: RoleUser, Content: "<@1>Alice: yo", AuthorID: "1", AuthorNick: "Alice"}},
		BotName: "Groq", MaxTokens: 200,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Content)
	assert.Equal(t, 2, calls)
}

func TestOllamaCompletionModeTemperatureWhenSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, 0.4, payload["options"].(map[string]any)["temperature"])
		_, _ = io.WriteString(w, `{"response":"hi"}`)
	}))
	defer server.Close()
	provider, err := NewOllamaProvider(server.URL, false)
	require.NoError(t, err)
	provider.SetCompletionMode(true)
	resp, err := provider.Chat(context.Background(), &ChatRequest{Model: "x", Temperature: 0.4, BotName: "Bot"})
	require.NoError(t, err)
	assert.Equal(t, "hi", resp.Content)
}
