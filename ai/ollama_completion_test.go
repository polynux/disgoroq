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
		{Role: RoleUser, Content: "<@1>Alice: quelqu'un a vu le match hier ?", Text: "quelqu'un a vu le match hier ?", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleAssistant, Content: "ouais trop nul", Text: "ouais trop nul", AuthorNick: "bot"},
		{Role: RoleUser, Content: "<@2>Bob: c’était chaud", Text: "c’était chaud", AuthorID: "2", AuthorNick: "Bob"},
		{Role: RoleUser, Content: "<@3>Léa: t'es d'accord ?", Text: "t'es d'accord ?", AuthorID: "3", AuthorNick: "Léa", MessageID: "target"},
	}
	got := buildCompletionPrompt(messages, "DisgoroQ", "target", "§bot", 2400, 4, "v0")
	want := "Alice: quelqu'un a vu le match hier ?\n§bot: ouais trop nul\nBob: c'était chaud\nLéa: t'es d'accord ?\n§bot:"
	assert.Equal(t, want, got)
}

func TestBuildCompletionPromptMovesTargetAfterNewerMessages(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: cible", Text: "cible", AuthorID: "1", AuthorNick: "Alice", MessageID: "target"},
		{Role: RoleUser, Content: "<@2>Bob: plus récent", Text: "plus récent", AuthorID: "2", AuthorNick: "Bob", MessageID: "newer"},
	}
	assert.Equal(t, "Bob: plus récent\nAlice: cible\nbot:", buildCompletionPrompt(messages, "bot", "target", "", 2400, 4, "v0"))
}

func TestBuildCompletionPromptCapsBotLines(t *testing.T) {
	var messages []Message
	for i := 1; i <= 6; i++ {
		messages = append(messages, Message{Role: RoleAssistant, Content: "bot line", Text: "bot line", AuthorNick: "bot"})
		messages = append(messages, Message{Role: RoleUser, Content: "Alice: human line", Text: "human line", AuthorNick: "Alice"})
	}
	prompt := buildCompletionPrompt(messages, "Display", "", "§bot", 2400, 4, "v0")
	assert.Equal(t, 4, strings.Count(prompt, "§bot: bot line"))
	assert.Equal(t, 6, strings.Count(prompt, "Alice: human line"))

	capped := buildCompletionPrompt(messages, "Display", "", "§bot", 2400, 1, "v0")
	assert.Equal(t, 1, strings.Count(capped, "§bot: bot line"))
}

func TestBuildCompletionPromptLegacySpeakerAndCleaning(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: regarde ça https://tenor.com/view/x <:kappa:42>\n\n", Text: "regarde ça https://tenor.com/view/x <:kappa:42>", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleAssistant, Content: "mdrrr", Text: "mdrrr", AuthorID: "9", AuthorNick: "Bot"},
		{Role: RoleUser, Content: "<@2>Bob: <@1> t'as\nvu\n\n", Text: "<@1> t'as\nvu", AuthorID: "2", AuthorNick: "Bob"},
		{Role: RoleUser, Content: "<@2>Bob: [Document Summary]\nrésumé\n\n", Text: "", AuthorID: "2", AuthorNick: "Bob"},
		{Role: "tool", Content: "ignored"},
	}
	assert.Equal(t, "Alice: regarde ça [gif] :kappa:\nBot: mdrrr\nBob: @Alice t'as vu\nBot:", buildCompletionPrompt(messages, "Bot", "", "", 2400, 4, "v0"))
}

func TestBuildCompletionPromptBudgetKeepsLatestAndTarget(t *testing.T) {
	var messages []Message
	for i := 0; i < 50; i++ {
		messages = append(messages, Message{Role: RoleUser, Content: "<@1>Alice: " + strings.Repeat("a", 100), Text: strings.Repeat("a", 100), AuthorID: "1", AuthorNick: "Alice"})
	}
	messages = append(messages, Message{Role: RoleUser, Content: "<@2>Bob: target", Text: "target", AuthorID: "2", AuthorNick: "Bob", MessageID: "target"})
	prompt := buildCompletionPrompt(messages, "bot", "target", "", 2400, 4, "v0")
	assert.LessOrEqual(t, len(prompt), 2400+len("\nbot:"))
	assert.True(t, strings.HasSuffix(prompt, "Bob: target\nbot:"))
	assert.Contains(t, prompt, strings.Repeat("a", 100))

	targetOnly := buildCompletionPrompt(messages, "bot", "target", "", 10, 4, "v0")
	assert.True(t, strings.Contains(targetOnly, "Bob: target\nbot:"))
}

func TestBuildCompletionPromptExcludesBotsCommandsAndErrorReplies(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<@1>Alice: human", Text: "human", AuthorID: "1", AuthorNick: "Alice"},
		{Role: RoleUser, Content: "<@2>OtherBot: bot chatter", Text: "bot chatter", AuthorID: "2", AuthorNick: "OtherBot", AuthorBot: true},
		{Role: RoleAssistant, Content: "mémoire désactivée", Text: "mémoire désactivée", AuthorNick: "Bot", CommandOutput: true},
		{Role: RoleAssistant, Content: "Désolé, tous mes systèmes sont en rade... 🤖💀", Text: "Désolé, tous mes systèmes sont en rade... 🤖💀", AuthorNick: "Bot"},
	}
	prompt := buildCompletionPrompt(messages, "Bot", "", "§bot", 2400, 4, "v0")
	assert.Equal(t, "Alice: human\n§bot:", prompt)
}

func TestBuildCompletionPromptKeepsMediaOnlyTarget(t *testing.T) {
	imageOnly := []Message{{
		Role: RoleUser, Content: "<@1>Alice: ", AuthorID: "1", AuthorNick: "Alice", MessageID: "target",
		Attachments: []MessageAttachment{{Filename: "IMG_8158.png", ContentType: "image/png"}},
		MediaOnly:   true,
	}}
	assert.Equal(t, "Alice: [image]\n§bot:", buildCompletionPrompt(imageOnly, "Bot", "target", "§bot", 2400, 4, "v0"))
	assert.Equal(t, "Alice: [image]\n§bot:", buildCompletionPrompt(imageOnly, "Bot", "target", "§bot", 2400, 4, "v1"))

	stickerOnly := []Message{{
		Role: RoleUser, Content: "<@1>Alice: ", AuthorID: "1", AuthorNick: "Alice", MessageID: "target",
		Stickers:  []string{"Wave"},
		MediaOnly: true,
	}}
	assert.Equal(t, "Alice: [image]\n§bot:", buildCompletionPrompt(stickerOnly, "Bot", "target", "§bot", 2400, 4, "v0"))
	assert.Equal(t, "Alice: [sticker: Wave]\n§bot:", buildCompletionPrompt(stickerOnly, "Bot", "target", "§bot", 2400, 4, "v1"))
}

func TestBuildCompletionPromptIncludesMediaWithText(t *testing.T) {
	messages := []Message{{
		Role: RoleUser, Content: "<@1>Alice: regarde ", Text: "regarde", AuthorID: "1", AuthorNick: "Alice",
		Attachments: []MessageAttachment{{Filename: "deepfry.gif", ContentType: "image/gif"}},
	}}
	assert.Equal(t, "Alice: regarde\n§bot:", buildCompletionPrompt(messages, "Bot", "", "§bot", 2400, 4, "v0"))
	assert.Equal(t, "Alice: regarde [gif: deepfry]\n§bot:", buildCompletionPrompt(messages, "Bot", "", "§bot", 2400, 4, "v1"))
}

func TestBuildCompletionPromptUsesMessageText(t *testing.T) {
	messages := []Message{
		{
			Role: RoleUser, Content: "<IMAGE_DESC>un chat</IMAGE_DESC>\n\n<@1>Alice: regarde ",
			Text: "regarde ", AuthorID: "1", AuthorNick: "Alice",
			Attachments: []MessageAttachment{{Filename: "image.png", ContentType: "image/png"}},
		},
		{
			Role: RoleUser, Content: "<@2>Bob: [Document Summary]\nrésumé\n\n",
			Text: "", AuthorID: "2", AuthorNick: "Bob",
			Attachments: []MessageAttachment{{Filename: "notes.txt", ContentType: "text/plain"}},
			MediaOnly:   true,
		},
	}
	assert.Equal(t, "Alice: regarde [image]\nBob: [fichier]\n§bot:",
		buildCompletionPrompt(messages, "Bot", "", "§bot", 2400, 4, "v1"))
	assert.Equal(t, "Alice: regarde\nBob: [image]\n§bot:",
		buildCompletionPrompt(messages, "Bot", "", "§bot", 2400, 4, "v0"))
}

func TestLimitCompletionLine(t *testing.T) {
	// 299 multibyte runes then a tag starting at the cap: the tag is dropped.
	assert.Equal(t, strings.Repeat("é", 299), limitCompletionLine(strings.Repeat("é", 299)+"[gif]", 300))
	// A complete tag inside the cap is kept.
	assert.Equal(t, "[gif: chat]", limitCompletionLine("[gif: chat]", 300))
	// Past the cap, cut at the last space.
	long := strings.Repeat("é", 290) + " " + strings.Repeat("a", 20)
	assert.Equal(t, strings.Repeat("é", 290), limitCompletionLine(long, 300))
	// A tag crossing the space cut is dropped entirely.
	crossing := strings.Repeat("é", 290) + " [gif: chat]" + strings.Repeat("a", 20)
	assert.Equal(t, strings.Repeat("é", 290), limitCompletionLine(crossing, 300))
}

func TestCompletionTargetIsBot(t *testing.T) {
	messages := []Message{
		{MessageID: "human", Role: RoleUser, AuthorNick: "Alice"},
		{MessageID: "bot", Role: RoleUser, AuthorNick: "OtherBot", AuthorBot: true},
	}
	assert.False(t, completionTargetIsBot(messages, "human"))
	assert.True(t, completionTargetIsBot(messages, "bot"))
	assert.False(t, completionTargetIsBot(messages, ""))
	assert.False(t, completionTargetIsBot(messages, "missing"))
}

func TestCompletionModeStaysSilentForBotTarget(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"response":"hi"}`)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL, false)
	require.NoError(t, err)
	provider.SetCompletionMode(true)
	response, err := provider.Chat(context.Background(), &ChatRequest{
		Model: "luciole-1b-discord1", BotName: "Bot", TargetMessageID: "target",
		Messages: []Message{
			{Role: RoleUser, Content: "<@1>Alice: yo", Text: "yo", AuthorID: "1", AuthorNick: "Alice"},
			{Role: RoleUser, Content: "<@2>OtherBot: hi", Text: "hi", AuthorID: "2", AuthorNick: "OtherBot", AuthorBot: true, MessageID: "target"},
		},
	})
	require.NoError(t, err)
	assert.True(t, response.EmptyCompletion)
	assert.Empty(t, response.Content)
	assert.Equal(t, "ollama", response.Provider)
	assert.Equal(t, 0, calls)
}

func TestCompletionModeStaysSilentWithoutRenderableTarget(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"response":"hi"}`)
	}))
	defer server.Close()

	provider, err := NewOllamaProvider(server.URL, false)
	require.NoError(t, err)
	provider.SetCompletionMode(true)

	base := []Message{
		{Role: RoleUser, Content: "<@1>Alice: yo", Text: "yo", AuthorID: "1", AuthorNick: "Alice"},
	}
	for _, request := range []*ChatRequest{
		{Model: "luciole", BotName: "Bot", TargetMessageID: "missing", Messages: base},
		{Model: "luciole", BotName: "Bot", TargetMessageID: "target", Messages: append(base, Message{
			Role: RoleUser, Content: "<@2>Bob: \n\n", Text: "", AuthorID: "2", AuthorNick: "Bob", MessageID: "target",
		})},
		{Model: "luciole", BotName: "Bot", TargetFromBot: true, TargetMessageID: "target", Messages: append(base, Message{
			Role: RoleUser, Content: "<@2>Bob: hi", Text: "hi", AuthorID: "2", AuthorNick: "Bob", MessageID: "target",
		})},
	} {
		response, err := provider.Chat(context.Background(), request)
		require.NoError(t, err)
		assert.True(t, response.EmptyCompletion)
		assert.Empty(t, response.Content)
	}
	assert.Equal(t, 0, calls)
}

func TestCleanCompletionReply(t *testing.T) {
	assert.Equal(t, "salut Bot mdr: x", cleanCompletionReply("Alice: salut §bot mdr: x", "Bot"))
	assert.Equal(t, "ptdr t'es grave", cleanCompletionReply(" @Yu ptdr t'es grave [gif]", "Bot"))
	assert.Equal(t, "", cleanCompletionReply(" @Deleted", "Bot"))
	// Raw replies start with a space because the prompt ends with "§bot:".
	assert.Equal(t, "ouais grave", cleanCompletionReply(" Bob: ouais grave", "Bot"))
	assert.Equal(t, "ok Bot", cleanCompletionReply(" §bot: ok §bot", "Bot"))
	assert.Equal(t, "", cleanCompletionReply(" §bot:", "Bot"))
	assert.Equal(t, "regarde", cleanCompletionReply(" regarde [gif: cat smile]", "Bot"))
	assert.Equal(t, "mdr", cleanCompletionReply(" mdr [youtube: some title]", "Bot"))
	assert.Equal(t, "x", cleanCompletionReply(" x [sticker: Wave]", "Bot"))
	assert.Equal(t, "y", cleanCompletionReply(" y [video] [fichier]", "Bot"))
	assert.Equal(t, "z", cleanCompletionReply(" z [gif] [lien] [image] [son] [steam] [tweet] [tiktok] [insta]", "Bot"))
	assert.Equal(t, "", cleanCompletionReply(" [gif: cat smile]", "Bot"))
	assert.Equal(t, "", cleanCompletionReply("[youtube: some title]", "Bot"))
	assert.Equal(t, "", cleanCompletionReply(" §bot: [gif: cat smile]", "Bot"))
}

type stubMediaResolver struct {
	reqs   []MediaResolveRequest
	result MediaResolveResult
	ok     bool
}

func (s *stubMediaResolver) Resolve(_ context.Context, req MediaResolveRequest) (MediaResolveResult, bool) {
	s.reqs = append(s.reqs, req)
	return s.result, s.ok
}

func TestOllamaCompletionMediaResolver(t *testing.T) {
	t.Run("text and tag appends url on its own line", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"response":" mdr [gif: cat smile]"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{
			result: MediaResolveResult{Tag: "[gif: cat smile]", URL: "https://tenor.com/view/cat-smile-1", Similarity: 1.0},
			ok:     true,
		}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model: "luciole", BotName: "Bot", BotID: "bot-1", GuildID: "g-1", ChannelID: "c-1",
			TargetMessageID: "m-1",
			Messages: []Message{{
				Role: RoleUser, MessageID: "m-1", AuthorID: "1", AuthorNick: "Alice",
				Text: "regarde https://tenor.com/view/dog-bark-gif-99",
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, "mdr\nhttps://tenor.com/view/cat-smile-1", resp.Content)
		assert.Equal(t, "https://tenor.com/view/cat-smile-1", resp.MediaURL)
		require.Len(t, resolver.reqs, 1)
		assert.Equal(t, "g-1", resolver.reqs[0].GuildID)
		assert.Equal(t, "c-1", resolver.reqs[0].ChannelID)
		assert.Equal(t, "bot-1", resolver.reqs[0].BotID)
		assert.Equal(t, "regarde [gif: dog bark]", resolver.reqs[0].TargetText)
	})

	t.Run("tag-only reply becomes whole url without retry", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = io.WriteString(w, `{"response":" [gif: cat smile]"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		provider.SetMediaResolver(&stubMediaResolver{
			result: MediaResolveResult{Tag: "[gif: cat smile]", URL: "https://tenor.com/view/cat-smile-1", Similarity: 0.92},
			ok:     true,
		})

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model: "luciole", BotName: "Bot", GuildID: "g-1",
			Messages: []Message{{Role: RoleUser, AuthorID: "1", AuthorNick: "Alice", Text: "yo"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "https://tenor.com/view/cat-smile-1", resp.Content)
		assert.Equal(t, "https://tenor.com/view/cat-smile-1", resp.MediaURL)
		assert.Equal(t, 1, calls)
	})

	t.Run("unmatched tag is stripped as today", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"response":" lol [gif: unknown]"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		provider.SetMediaResolver(&stubMediaResolver{ok: false})

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model: "luciole", BotName: "Bot", GuildID: "g-1",
			Messages: []Message{{Role: RoleUser, AuthorID: "1", AuthorNick: "Alice", Text: "yo"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "lol", resp.Content)
		assert.Empty(t, resp.MediaURL)
	})

	t.Run("text-only reply keeps model text and appends resolved gif_on_request url", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"response":" tiens bg"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{
			result: MediaResolveResult{Tag: "gif_on_request", URL: "https://tenor.com/view/cat-smile-1"},
			ok:     true,
		}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model: "luciole", BotName: "Bot", GuildID: "g-1",
			TargetMessageID: "m-1",
			Messages:        []Message{{Role: RoleUser, MessageID: "m-1", AuthorID: "1", AuthorNick: "Alice", Text: "envoie un gif stp"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "tiens bg\nhttps://tenor.com/view/cat-smile-1", resp.Content)
		assert.Equal(t, "https://tenor.com/view/cat-smile-1", resp.MediaURL)
		require.Len(t, resolver.reqs, 1)
		assert.Equal(t, "envoie un gif stp", resolver.reqs[0].TargetText)
		assert.Equal(t, "m-1", resolver.reqs[0].TargetMessageID)
	})

	t.Run("reengage leaves TargetMessageID empty while keeping TargetText", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"response":" coucou [gif]"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{
			result: MediaResolveResult{Tag: "[gif]", URL: "https://tenor.com/view/cat-smile-1"},
			ok:     true,
		}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model: "luciole", BotName: "Bot", GuildID: "g-1",
			Messages: []Message{{Role: RoleUser, MessageID: "old-1", AuthorID: "1", AuthorNick: "Alice", Text: "envoie un gif stp"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "coucou\nhttps://tenor.com/view/cat-smile-1", resp.Content)
		require.Len(t, resolver.reqs, 1)
		assert.Empty(t, resolver.reqs[0].TargetMessageID)
		assert.Equal(t, "envoie un gif stp", resolver.reqs[0].TargetText)
	})

	t.Run("empty tagless attempt 0 retries before calling resolver", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				_, _ = io.WriteString(w, `{"response":" @Deleted"}`)
				return
			}
			_, _ = io.WriteString(w, `{"response":" voilà"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{
			result: MediaResolveResult{Tag: "gif_on_request", URL: "https://tenor.com/view/cat-smile-1"},
			ok:     true,
		}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), &ChatRequest{
			Model: "luciole", BotName: "Bot", GuildID: "g-1",
			TargetMessageID: "m-1",
			Messages:        []Message{{Role: RoleUser, MessageID: "m-1", AuthorID: "1", AuthorNick: "Alice", Text: "un gif ?"}},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
		assert.Len(t, resolver.reqs, 1)
		assert.Equal(t, "m-1", resolver.reqs[0].TargetMessageID)
		assert.Equal(t, "voilà\nhttps://tenor.com/view/cat-smile-1", resp.Content)
		assert.Equal(t, "https://tenor.com/view/cat-smile-1", resp.MediaURL)
	})
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
		Model: "luciole-1b-discord1", Messages: []Message{{Role: RoleUser, Content: "<@1>Alice: yo", Text: "yo", AuthorID: "1", AuthorNick: "Alice"}},
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

func TestTrimCompletionSentence(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{
			name:   "period followed by space",
			input:  " C'est vraiment la fin du monde. Et encore j'ai pas fini de",
			want:   "C'est vraiment la fin du monde.",
			wantOK: true,
		},
		{
			name:   "period at end of text",
			input:  " C'est vraiment la fin du monde. ",
			want:   "C'est vraiment la fin du monde.",
			wantOK: true,
		},
		{
			name:   "exclamation mark",
			input:  " Incroyable cette histoire ! Mais attends la suite c'est",
			want:   "Incroyable cette histoire !",
			wantOK: true,
		},
		{
			name:   "question mark",
			input:  " Tu penses vraiment que ça va marcher ? Parce que moi je",
			want:   "Tu penses vraiment que ça va marcher ?",
			wantOK: true,
		},
		{
			name:   "unicode ellipsis",
			input:  " Franchement je sais plus quoi dire… et puis en vrai",
			want:   "Franchement je sais plus quoi dire…",
			wantOK: true,
		},
		{
			name:   "triple dot run",
			input:  " Franchement je sais plus quoi dire... et puis en vrai",
			want:   "Franchement je sais plus quoi dire...",
			wantOK: true,
		},
		{
			name:   "question exclamation run",
			input:  " T'as vraiment osé faire ça ?! Non mais sérieusement",
			want:   "T'as vraiment osé faire ça ?!",
			wantOK: true,
		},
		{
			name:   "multiple exclamations run",
			input:  " C'est n'importe quoi cette game !!! On aurait jamais dû",
			want:   "C'est n'importe quoi cette game !!!",
			wantOK: true,
		},
		{
			name:   "decimal number not a boundary",
			input:  " Il fait au moins 3.5 degrés dehors aujourd'hui et j'ai",
			want:   "",
			wantOK: false,
		},
		{
			name:   "url dot not a boundary",
			input:  " Regarde sur https://example.com/foo.bar et dis moi ce que tu",
			want:   "",
			wantOK: false,
		},
		{
			name:   "too short after trimming",
			input:  " Trop court. Mais la suite continue beaucoup plus loin sans fin",
			want:   "",
			wantOK: false,
		},
		{
			name:   "unterminated media tag after complete sentence",
			input:  " C'est vraiment trop drôle cette histoire ! [gif: chat qui tombe",
			want:   "C'est vraiment trop drôle cette histoire !",
			wantOK: true,
		},
		{
			name:   "period inside unterminated media tag ignored",
			input:  " C'est vraiment trop drôle [gif: chat. qui tombe",
			want:   "",
			wantOK: false,
		},
		{
			name:   "unterminated media tag without sentence boundary",
			input:  " C'est vraiment trop drôle cette histoire [gif: chat qui",
			want:   "",
			wantOK: false,
		},
		{
			name:   "punctuation inside a closed media tag is not a boundary",
			input:  " Regarde ça [youtube: Incroyable ! Le pire fail] et franchement j'ai pas",
			want:   "",
			wantOK: false,
		},
		{
			name:   "sentence before a closed media tag with inner punctuation",
			input:  " Tu vas adorer cette vidéo. [youtube: Incroyable ! Le pire fail] et franchement",
			want:   "Tu vas adorer cette vidéo.",
			wantOK: true,
		},
		{
			name:   "stray closing bracket does not hide earlier sentence",
			input:  " Bon ben on verra bien demain. c'est cool :] et puis",
			want:   "Bon ben on verra bien demain.",
			wantOK: true,
		},
		{
			name:   "stray closing bracket after a closed tag",
			input:  " [gif: chat] Bon ben on verra bien demain. c'est cool :] et puis",
			want:   "[gif: chat] Bon ben on verra bien demain.",
			wantOK: true,
		},
		{
			name:   "sentence before a closed tag and a stray bracket",
			input:  " C'est vraiment trop drôle cette histoire ! [gif: chat] ] et la suite continue",
			want:   "C'est vraiment trop drôle cette histoire !",
			wantOK: true,
		},
		{
			name:   "multibyte accents and emoji counted as runes",
			input:  " C'est déjà très été 🔥! Et ensuite on verra bien",
			want:   "C'est déjà très été 🔥!",
			wantOK: true,
		},
		{
			name:   "multibyte under 20 runes rejected despite >20 bytes",
			input:  " été 🔥🔥🔥🔥! Et ensuite on continue sans fin",
			want:   "",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := trimCompletionSentence(tc.input, completionMinSentenceRunes)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCompletionChatTruncatedReplies(t *testing.T) {
	baseReq := func() *ChatRequest {
		return &ChatRequest{
			Model: "luciole-1b-discord1", BotName: "Bot", GuildID: "g-1", ChannelID: "c-1",
			TargetMessageID: "m-1",
			Messages: []Message{{
				Role: RoleUser, MessageID: "m-1", AuthorID: "1", AuthorNick: "Alice",
				Text: "tu en penses quoi ?",
			}},
		}
	}

	t.Run("length with usable sentence boundary trims without retry", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = io.WriteString(w, `{"model":"luciole","response":" C'est vraiment la fin du monde. Et encore j'ai pas fini de","done":true,"done_reason":"length"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{ok: false}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), baseReq())
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
		assert.Equal(t, "C'est vraiment la fin du monde.", resp.Content)
		assert.Equal(t, "length", resp.FinishReason)
		require.Len(t, resolver.reqs, 1)
		assert.Equal(t, "C'est vraiment la fin du monde.", resolver.reqs[0].RawReply)
	})

	t.Run("length without boundary retries and skips resolver on discarded attempt", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				_, _ = io.WriteString(w, `{"model":"luciole","response":" c'est vraiment la fin du mon [gif: chat]","done":true,"done_reason":"length"}`)
				return
			}
			_, _ = io.WriteString(w, `{"model":"luciole","response":" carrément d'accord","done":true,"done_reason":"stop"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{
			result: MediaResolveResult{Tag: "[gif: chat]", URL: "https://tenor.com/view/cat-1"},
			ok:     true,
		}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), baseReq())
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
		require.Len(t, resolver.reqs, 1)
		assert.Equal(t, " carrément d'accord", resolver.reqs[0].RawReply)
		assert.Equal(t, "carrément d'accord\nhttps://tenor.com/view/cat-1", resp.Content)
	})

	t.Run("length still truncated on last attempt falls back to word boundary and drops open tag", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				_, _ = io.WriteString(w, `{"model":"luciole","response":" premier essai coupé sans ponctuation du tout","done":true,"done_reason":"length"}`)
				return
			}
			_, _ = io.WriteString(w, `{"model":"luciole","response":" deuxième essai coupé au milieu [gif: chat qui","done":true,"done_reason":"length"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{ok: false}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), baseReq())
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
		assert.Equal(t, "deuxième essai coupé au milieu", resp.Content)
		assert.False(t, resp.EmptyCompletion)
		require.Len(t, resolver.reqs, 1)
		assert.Equal(t, "deuxième essai coupé au milieu", resolver.reqs[0].RawReply)
	})

	t.Run("length on last attempt keeps a reply that ends on a complete media tag", func(t *testing.T) {
		for _, tc := range []struct{ name, second, wantRaw, wantContent string }{
			{"tag only", " [gif: chat qui tombe]", "[gif: chat qui tombe]", "https://tenor.com/view/cat-1"},
			{"text then tag", " trop drôle cette vidéo mdr [gif: chat qui tombe]", "trop drôle cette vidéo mdr [gif: chat qui tombe]", "trop drôle cette vidéo mdr\nhttps://tenor.com/view/cat-1"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					response := " premier essai coupé sans ponctuation du tout"
					if calls == 2 {
						response = tc.second
					}
					body, _ := json.Marshal(map[string]any{"model": "luciole", "response": response, "done": true, "done_reason": "length"})
					_, _ = w.Write(body)
				}))
				defer server.Close()

				provider, err := NewOllamaProvider(server.URL, false)
				require.NoError(t, err)
				provider.SetCompletionMode(true)
				resolver := &stubMediaResolver{
					result: MediaResolveResult{Tag: "[gif: chat qui tombe]", URL: "https://tenor.com/view/cat-1"},
					ok:     true,
				}
				provider.SetMediaResolver(resolver)

				resp, err := provider.Chat(context.Background(), baseReq())
				require.NoError(t, err)
				assert.Equal(t, 2, calls)
				require.Len(t, resolver.reqs, 1)
				assert.Equal(t, tc.wantRaw, resolver.reqs[0].RawReply)
				assert.Equal(t, tc.wantContent, resp.Content)
				assert.Equal(t, "length", resp.FinishReason)
				assert.False(t, resp.EmptyCompletion)
			})
		}
	})

	t.Run("length trimmed to a sentence keeps complete media tags after it", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"model":"luciole","response":" C'est vraiment trop drôle cette histoire ! [gif: chat qui tombe] et puis franchement j'ai","done":true,"done_reason":"length"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)
		resolver := &stubMediaResolver{
			result: MediaResolveResult{Tag: "[gif: chat qui tombe]", URL: "https://tenor.com/view/cat-1"},
			ok:     true,
		}
		provider.SetMediaResolver(resolver)

		resp, err := provider.Chat(context.Background(), baseReq())
		require.NoError(t, err)
		require.Len(t, resolver.reqs, 1)
		assert.Equal(t, "C'est vraiment trop drôle cette histoire ! [gif: chat qui tombe]", resolver.reqs[0].RawReply)
		assert.Equal(t, "C'est vraiment trop drôle cette histoire !\nhttps://tenor.com/view/cat-1", resp.Content)
	})

	t.Run("length on last attempt still word-cuts a trailing bracket that is not a media tag", func(t *testing.T) {
		for _, tc := range []struct{ name, second, want string }{
			{"stray bracket", " du blabla sans ponctuation qui finit par un crochet ]", "du blabla sans ponctuation qui finit par un crochet"},
			{"bracketed words", " premier essai coupé [pas un tag]", "premier essai coupé"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					response := " premier essai coupé sans ponctuation du tout"
					if calls == 2 {
						response = tc.second
					}
					body, _ := json.Marshal(map[string]any{"model": "luciole", "response": response, "done": true, "done_reason": "length"})
					_, _ = w.Write(body)
				}))
				defer server.Close()

				provider, err := NewOllamaProvider(server.URL, false)
				require.NoError(t, err)
				provider.SetCompletionMode(true)

				resp, err := provider.Chat(context.Background(), baseReq())
				require.NoError(t, err)
				assert.Equal(t, 2, calls)
				assert.Equal(t, tc.want, resp.Content)
			})
		}
	})

	t.Run("stop done_reason is unchanged even without punctuation", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = io.WriteString(w, `{"model":"luciole","response":" ouais carrément","done":true,"done_reason":"stop"}`)
		}))
		defer server.Close()

		provider, err := NewOllamaProvider(server.URL, false)
		require.NoError(t, err)
		provider.SetCompletionMode(true)

		resp, err := provider.Chat(context.Background(), baseReq())
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
		assert.Equal(t, "ouais carrément", resp.Content)
	})
}

func TestBuildCompletionPromptBotLineCapAndDefault(t *testing.T) {
	t.Run("400-rune bot line capped at sentence boundary within 200 runes while human keeps 300", func(t *testing.T) {
		botSentence := "C'est une première phrase complète et assez longue pour passer le seuil."
		botLong := botSentence + " " + strings.Repeat("mot ", 90)
		require.Greater(t, len([]rune(botLong)), 400)

		humanLong := strings.Repeat("mot ", 100)
		require.Greater(t, len([]rune(humanLong)), 350)

		messages := []Message{
			{Role: RoleAssistant, Content: botLong, Text: botLong, AuthorNick: "bot"},
			{Role: RoleUser, Content: "Alice: " + humanLong, Text: humanLong, AuthorID: "1", AuthorNick: "Alice"},
		}
		prompt := buildCompletionPrompt(messages, "Display", "", "§bot", 2400, 0, "v0")
		lines := strings.Split(prompt, "\n")
		require.Len(t, lines, 3)

		botLine := strings.TrimPrefix(lines[0], "§bot: ")
		assert.Equal(t, botSentence, botLine)
		assert.LessOrEqual(t, len([]rune(botLine)), completionBotLineChars)

		humanLine := strings.TrimPrefix(lines[1], "Alice: ")
		assert.Greater(t, len([]rune(humanLine)), completionBotLineChars)
		assert.LessOrEqual(t, len([]rune(humanLine)), completionLineChars)
	})

	t.Run("400-rune bot line without sentence boundary falls back to word boundary <= 200 runes", func(t *testing.T) {
		botNoPunct := strings.Repeat("blabla ", 60)
		require.Greater(t, len([]rune(botNoPunct)), 400)

		messages := []Message{
			{Role: RoleAssistant, Content: botNoPunct, Text: botNoPunct, AuthorNick: "bot"},
		}
		prompt := buildCompletionPrompt(messages, "Display", "", "§bot", 2400, 0, "v0")
		lines := strings.Split(prompt, "\n")
		require.Len(t, lines, 2)

		botLine := strings.TrimPrefix(lines[0], "§bot: ")
		assert.LessOrEqual(t, len([]rune(botLine)), completionBotLineChars)
		assert.Equal(t, strings.TrimSpace(strings.Repeat("blabla ", 28)), botLine)
	})

	t.Run("punctuation at the 200-rune cut", func(t *testing.T) {
		head := strings.Repeat("a", 30) + " " + strings.Repeat("b", 168) // 199 runes
		for _, tc := range []struct{ name, tail, want string }{
			// Rune 200 is "." and rune 201 a space: the sentence ends exactly at the cap.
			{"followed by space is a boundary", ". suite du texte qui continue encore", head + "."},
			// Rune 200 is "." but rune 201 is "5": "3.5"-style, not a sentence end.
			{"followed by a digit is not a boundary", ".5 degrés et la suite continue encore", strings.Repeat("a", 30)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				line := head + tc.tail
				require.Equal(t, '.', []rune(line)[199])
				assert.Equal(t, tc.want, limitCompletionBotLine(line, completionBotLineChars))
			})
		}
	})

	t.Run("default keeps 8 bot lines when character budget allows", func(t *testing.T) {
		var messages []Message
		for i := 1; i <= 10; i++ {
			messages = append(messages, Message{Role: RoleAssistant, Content: "bot line", Text: "bot line", AuthorNick: "bot"})
			messages = append(messages, Message{Role: RoleUser, Content: "Alice: human line", Text: "human line", AuthorNick: "Alice"})
		}
		prompt := buildCompletionPrompt(messages, "Display", "", "§bot", 2400, 0, "v0")
		assert.Equal(t, 8, strings.Count(prompt, "§bot: bot line"))
		assert.Equal(t, 10, strings.Count(prompt, "Alice: human line"))
	})
}
