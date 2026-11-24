package handlers

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/utils"
)

func TestMessageHandlerHotAppliesRuntimeTriggerWordsAndPrompt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := utils.OpenLocalDB(dbPath)
	if err != nil {
		t.Fatalf("OpenLocalDB() error = %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	_, err = sqlDB.Exec(`CREATE TABLE guild_settings (guild_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (guild_id, key))`)
	if err != nil {
		t.Fatalf("create guild_settings error = %v", err)
	}
	repo := database.NewRepositoryWithDB(sqlDB)

	initial := config.DefaultConfig()
	initial.Bot.DefaultPrompt = "hello {{.BotNick}} v1"
	initial.Bot.TriggerWords = []string{"alpha"}
	runtime := config.NewRuntime(initial)

	h := NewMessageHandler(nil, &ai.Service{}, repo, nil, nil, runtime)

	guildID := snowflake.ID(999)
	msgAlpha := &discord.Message{GuildID: &guildID, Content: "hey alpha!"}
	msgBeta := &discord.Message{GuildID: &guildID, Content: "hey beta!"}

	if !h.isExplicitTrigger(context.Background(), msgAlpha, snowflake.ID(1)) {
		t.Fatal("expected 'alpha' to trigger before runtime update")
	}
	if h.isExplicitTrigger(context.Background(), msgBeta, snowflake.ID(1)) {
		t.Fatal("did not expect 'beta' to trigger before runtime update")
	}
	if got := h.GetDefaultPrompt("Bot"); got != "hello Bot v1" {
		t.Fatalf("GetDefaultPrompt() = %q, want %q", got, "hello Bot v1")
	}

	// Publish updated config snapshot to Runtime.
	updated := config.DefaultConfig()
	updated.Bot.DefaultPrompt = "hello {{.BotNick}} v2"
	updated.Bot.TriggerWords = []string{"Beta"}
	runtime.Store(updated)

	if h.isExplicitTrigger(context.Background(), msgAlpha, snowflake.ID(1)) {
		t.Fatal("did not expect 'alpha' to trigger after runtime update")
	}
	if !h.isExplicitTrigger(context.Background(), msgBeta, snowflake.ID(1)) {
		t.Fatal("expected 'beta' to trigger after runtime update")
	}
	if got := h.GetDefaultPrompt("Bot"); got != "hello Bot v2" {
		t.Fatalf("GetDefaultPrompt() = %q, want %q", got, "hello Bot v2")
	}
}

func TestVoteHandlerHotAppliesRuntimeVotesConfig(t *testing.T) {
	initial := config.DefaultConfig()
	initial.Votes = config.VotesConfig{
		Enabled:   true,
		GoodEmoji: "😂",
		BadEmoji:  "🗑️",
	}
	runtime := config.NewRuntime(initial)
	h := NewVoteHandler(nil, nil, runtime)

	if !h.isVoteEmoji("😂") {
		t.Fatal("expected 😂 to be recognized before runtime update")
	}
	if h.isVoteEmoji("🔥") {
		t.Fatal("did not expect 🔥 to be recognized before runtime update")
	}

	updated := config.DefaultConfig()
	updated.Votes = config.VotesConfig{
		Enabled:   true,
		GoodEmoji: "🔥",
		BadEmoji:  "💩",
	}
	runtime.Store(updated)

	if h.isVoteEmoji("😂") {
		t.Fatal("did not expect 😂 to be recognized after runtime update")
	}
	if !h.isVoteEmoji("🔥") {
		t.Fatal("expected 🔥 to be recognized after runtime update")
	}
}
