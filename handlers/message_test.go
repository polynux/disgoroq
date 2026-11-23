package handlers

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/medialibrary"
)

func TestTryAcquireReplySlotIsBounded(t *testing.T) {
	h := &MessageHandler{replySlots: make(chan struct{}, 1)}

	if !h.tryAcquireReplySlot() {
		t.Fatal("expected the first reply slot acquisition to succeed")
	}
	if h.tryAcquireReplySlot() {
		t.Fatal("expected acquisition to fail while the slot is occupied")
	}

	<-h.replySlots
	if !h.tryAcquireReplySlot() {
		t.Fatal("expected acquisition to succeed after the slot is released")
	}
}

func TestLockForSharesOneLockPerKey(t *testing.T) {
	h := &MessageHandler{}

	first := h.lockFor(&h.replyLocks, "1")
	if first != h.lockFor(&h.replyLocks, "1") {
		t.Fatal("expected the same key to share a lock")
	}
	if first == h.lockFor(&h.replyLocks, "2") {
		t.Fatal("expected different keys to use separate locks")
	}
}

func TestChatSessionID(t *testing.T) {
	guildID := snowflake.ID(123456789)
	channelID := snowflake.ID(987654321)

	if got := chatSessionID(&guildID, channelID); got != "123456789:987654321" {
		t.Fatalf("chatSessionID(guild, channel) = %q, want %q", got, "123456789:987654321")
	}
	if got := chatSessionID(nil, channelID); got != "987654321" {
		t.Fatalf("chatSessionID(nil, channel) = %q, want %q", got, "987654321")
	}
	if got := chatSessionID(&guildID, 0); got != "" {
		t.Fatalf("chatSessionID(guild, 0) = %q, want empty", got)
	}
}

func TestSkipCompletionTarget(t *testing.T) {
	newService := func(completion bool) *ai.Service {
		return ai.NewService(ai.ServiceConfig{
			PrimaryProvider:          ai.ProviderOllama,
			OllamaEnabled:            true,
			OllamaURL:                "http://localhost:11434",
			OllamaModel:              "luciole-1b-discord1",
			OllamaVisionModel:        "llava",
			OllamaCompletion:         completion,
			RetryConfig:              ai.DefaultRetryConfig(),
			MinResponseLength:        1,
			OllamaCompletionBotLines: 4,
		})
	}

	botMessage := discord.Message{Author: discord.User{ID: snowflake.ID(1), Bot: true}}
	webhookID := snowflake.ID(2)
	webhookMessage := discord.Message{WebhookID: &webhookID}
	humanMessage := discord.Message{Author: discord.User{ID: snowflake.ID(3)}}

	completionHandler := &MessageHandler{aiservice: newService(true)}
	if !completionHandler.skipCompletionTarget(botMessage) {
		t.Fatal("completion mode must skip another bot's message")
	}
	if !completionHandler.skipCompletionTarget(webhookMessage) {
		t.Fatal("completion mode must skip a webhook message")
	}
	if completionHandler.skipCompletionTarget(humanMessage) {
		t.Fatal("completion mode must answer a human message")
	}

	chatHandler := &MessageHandler{aiservice: newService(false)}
	if chatHandler.skipCompletionTarget(botMessage) {
		t.Fatal("chat mode must keep its current bot behaviour")
	}
}

func TestSkipCompletionTargetUsesPrimaryProviderWithFallback(t *testing.T) {
	botMessage := discord.Message{Author: discord.User{ID: snowflake.ID(1), Bot: true}}

	completionPrimary := ai.NewService(ai.ServiceConfig{
		PrimaryProvider:          ai.ProviderOllama,
		OllamaEnabled:            true,
		OllamaURL:                "http://localhost:11434",
		OllamaModel:              "luciole-1b-discord1",
		OllamaVisionModel:        "llava",
		OllamaCompletion:         true,
		OllamaCompletionBotLines: 4,
		GroqAPIKey:               "test-key",
		GroqModel:                "test-model",
		GroqVisionModel:          "test-vision-model",
		FallbackEnabled:          true,
		RetryConfig:              ai.DefaultRetryConfig(),
		MinResponseLength:        1,
	})
	if completionPrimary == nil {
		t.Fatal("failed to build the Ollama-primary service")
	}
	if !(&MessageHandler{aiservice: completionPrimary}).skipCompletionTarget(botMessage) {
		t.Fatalf("completion primary with chain %q must skip a bot message", completionPrimary.Name())
	}

	completionFallback := ai.NewService(ai.ServiceConfig{
		PrimaryProvider:          ai.ProviderGroq,
		GroqAPIKey:               "test-key",
		GroqModel:                "test-model",
		GroqVisionModel:          "test-vision-model",
		OllamaEnabled:            true,
		OllamaURL:                "http://localhost:11434",
		OllamaModel:              "luciole-1b-discord1",
		OllamaVisionModel:        "llava",
		OllamaCompletion:         true,
		OllamaCompletionBotLines: 4,
		FallbackEnabled:          true,
		RetryConfig:              ai.DefaultRetryConfig(),
		MinResponseLength:        1,
	})
	if completionFallback == nil {
		t.Fatal("failed to build the Groq-primary service")
	}
	if (&MessageHandler{aiservice: completionFallback}).skipCompletionTarget(botMessage) {
		t.Fatalf("non-Ollama primary with chain %q must keep chat behaviour", completionFallback.Name())
	}
}

func TestPostProcessReplyPreservesMediaURL(t *testing.T) {
	h := &MessageHandler{}
	mediaURL := "https://tenor.com/view/chauffeur-coiffeur-12345"

	got := h.postProcessReply("quoi feur\n"+mediaURL, mediaURL, "g1")
	want := "quoi fleur\n" + mediaURL
	if got != want {
		t.Fatalf("postProcessReply(text+url) = %q, want %q", got, want)
	}

	gotOnly := h.postProcessReply(mediaURL, mediaURL, "g1")
	if gotOnly != mediaURL {
		t.Fatalf("postProcessReply(url only) = %q, want %q", gotOnly, mediaURL)
	}

	gotNoMedia := h.postProcessReply("quoi feur", "", "g1")
	if gotNoMedia != "quoi fleur" {
		t.Fatalf("postProcessReply(no media) = %q, want %q", gotNoMedia, "quoi fleur")
	}
}

func TestHandleMessageUpdateIndexesUnfurledEmbeds(t *testing.T) {
	conn, err := sql.Open("libsql", "file:"+filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	schemaBytes, err := os.ReadFile(filepath.Join("..", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}
	for _, stmt := range strings.Split(string(schemaBytes), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.Contains(stmt, "libsql_vector_idx") {
			continue
		}
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("exec schema stmt: %v", err)
		}
	}

	repo := medialibrary.NewRepository(conn)
	svc := medialibrary.NewService(repo, nil, config.MediaLibraryConfig{Enabled: true})
	h := &MessageHandler{mediaLibrary: svc}

	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	humanMsg := discord.Message{
		ID:        snowflake.ID(3001),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: snowflake.ID(4001), Username: "alice"},
		Content:   "https://www.youtube.com/watch?v=handler1",
		Embeds: []discord.Embed{
			{URL: "https://www.youtube.com/watch?v=handler1", Title: "Handler Video"},
		},
		CreatedAt: time.Unix(1700000000, 0),
	}
	botMsg := discord.Message{
		ID:        snowflake.ID(3002),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: snowflake.ID(4002), Username: "bot", Bot: true},
		Content:   "https://www.youtube.com/watch?v=botvid",
		Embeds: []discord.Embed{
			{URL: "https://www.youtube.com/watch?v=botvid", Title: "Bot Video"},
		},
		CreatedAt: time.Unix(1700000001, 0),
	}

	// Send update twice for the same human message + once for a bot message.
	h.HandleMessageUpdate(&events.MessageUpdate{
		GenericMessage: &events.GenericMessage{MessageID: humanMsg.ID, ChannelID: chID, GuildID: &gID, Message: humanMsg},
	})
	h.HandleMessageUpdate(&events.MessageUpdate{
		GenericMessage: &events.GenericMessage{MessageID: humanMsg.ID, ChannelID: chID, GuildID: &gID, Message: humanMsg},
	})
	h.HandleMessageUpdate(&events.MessageUpdate{
		GenericMessage: &events.GenericMessage{MessageID: botMsg.ID, ChannelID: chID, GuildID: &gID, Message: botMsg},
	})

	// Close drains the bounded worker before checking the DB.
	svc.Close()

	entry, err := repo.GetEntry(context.Background(), "https://www.youtube.com/watch?v=handler1", gID.String())
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry == nil {
		t.Fatal("expected unfurled YouTube embed to be indexed")
	}
	if entry.Description != "Handler Video" || entry.Uses != 1 {
		t.Fatalf("got description=%q uses=%d, want %q and 1", entry.Description, entry.Uses, "Handler Video")
	}

	botEntry, err := repo.GetEntry(context.Background(), "https://www.youtube.com/watch?v=botvid", gID.String())
	if err != nil {
		t.Fatalf("GetEntry(bot): %v", err)
	}
	if botEntry != nil {
		t.Fatalf("expected bot embed not to be indexed, got %+v", botEntry)
	}
}


