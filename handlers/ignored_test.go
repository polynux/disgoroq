package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/medialibrary"
)

func openSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("libsql", "file:"+filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
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
	return conn
}

func TestHandleMessageUpdateSkipsIgnoredChannel(t *testing.T) {
	conn := openSchemaDB(t)
	repo := database.NewRepositoryWithDB(conn)
	mediaRepo := medialibrary.NewRepository(conn)
	svc := medialibrary.NewService(mediaRepo, nil, config.MediaLibraryConfig{Enabled: true})
	h := &MessageHandler{repo: repo, mediaLibrary: svc}

	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	if _, err := repo.AddIgnoredChannel(context.Background(), gID.String(), chID.String()); err != nil {
		t.Fatalf("AddIgnoredChannel: %v", err)
	}
	msg := discord.Message{
		ID:        snowflake.ID(3001),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: snowflake.ID(4001), Username: "alice"},
		Content:   "https://www.youtube.com/watch?v=ignored1",
		Embeds:    []discord.Embed{{URL: "https://www.youtube.com/watch?v=ignored1", Title: "Ignored Video"}},
		CreatedAt: time.Unix(1700000000, 0),
	}
	h.HandleMessageUpdate(&events.MessageUpdate{
		GenericMessage: &events.GenericMessage{MessageID: msg.ID, ChannelID: chID, GuildID: &gID, Message: msg},
	})
	svc.Close()

	entry, err := mediaRepo.GetEntry(context.Background(), "https://www.youtube.com/watch?v=ignored1", gID.String())
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry != nil {
		t.Fatalf("expected no media entry for an ignored channel, got %+v", entry)
	}
	var cached int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM discord_messages`).Scan(&cached); err != nil {
		t.Fatalf("count discord_messages: %v", err)
	}
	if cached != 0 {
		t.Fatalf("discord_messages rows = %d, want 0 for an ignored channel", cached)
	}
}

func TestVoteRecountSkipsIgnoredChannel(t *testing.T) {
	repo := database.NewRepositoryWithDB(openSchemaDB(t))
	gID, chID, msgID := snowflake.ID(1000), snowflake.ID(2000), snowflake.ID(3000)
	if _, err := repo.AddIgnoredChannel(context.Background(), gID.String(), chID.String()); err != nil {
		t.Fatalf("AddIgnoredChannel: %v", err)
	}
	handler := NewVoteHandler(nil, repo, config.NewRuntime(&config.Config{Votes: config.VotesConfig{Enabled: true}}))
	called := make(chan struct{}, 1)
	handler.recountFn = func(_, _, _ snowflake.ID, _ bool) { called <- struct{}{} }

	handler.recount(gID, chID, msgID, true)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		handler.recountMu.Lock()
		_, pending := handler.recounts[msgID]
		handler.recountMu.Unlock()
		if !pending {
			break
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-called:
		t.Fatal("recount ran for an ignored channel")
	default:
	}
	handler.recountMu.Lock()
	defer handler.recountMu.Unlock()
	if _, pending := handler.recounts[msgID]; pending {
		t.Fatal("recount state was not cleared for an ignored channel")
	}
}

func TestHandleMessageCreateSkipsIgnoredChannelAndChildThread(t *testing.T) {
	conn := openSchemaDB(t)
	repo := database.NewRepositoryWithDB(conn)
	h := &MessageHandler{repo: repo}

	gID := snowflake.ID(100000000000000001)
	parentID := snowflake.ID(200000000000000001)
	threadID := snowflake.ID(300000000000000001)
	if _, err := repo.AddIgnoredChannel(context.Background(), gID.String(), parentID.String()); err != nil {
		t.Fatalf("AddIgnoredChannel: %v", err)
	}

	caches := cache.New(cache.WithCaches(cache.FlagChannels))
	var thread discord.GuildThread
	if err := json.Unmarshal([]byte(`{"id":"`+threadID.String()+`","guild_id":"`+gID.String()+`","parent_id":"`+parentID.String()+`","name":"t","type":11}`), &thread); err != nil {
		t.Fatalf("unmarshal thread: %v", err)
	}
	caches.AddChannel(thread)
	// With no self user cached, client.ID() is 0, so author 0 is the bot itself.
	client := &bot.Client{Caches: caches}

	send := func(id snowflake.ID, channelID snowflake.ID, authorID snowflake.ID) {
		h.HandleMessageCreate(&events.MessageCreate{GenericMessage: &events.GenericMessage{
			GenericEvent: events.NewGenericEvent(client, 0, 0),
			MessageID:    id,
			ChannelID:    channelID,
			GuildID:      &gID,
			Message: discord.Message{
				ID: id, GuildID: &gID, ChannelID: channelID,
				Author:    discord.User{ID: authorID, Username: "u"},
				Content:   "hello",
				CreatedAt: time.Unix(1700000000, 0),
			},
		}})
	}
	send(4001, parentID, 5001) // human message in the ignored channel
	send(4002, threadID, 5001) // human message in a thread under it
	send(4003, parentID, 0)    // the bot's own message in the ignored channel

	var cached int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM discord_messages`).Scan(&cached); err != nil {
		t.Fatalf("count discord_messages: %v", err)
	}
	if cached != 0 {
		t.Fatalf("discord_messages rows = %d, want 0 for an ignored channel", cached)
	}
	for _, ch := range []snowflake.ID{parentID, threadID} {
		if got := repo.GetChannelLastMessage(context.Background(), gID.String(), ch.String()); got != 0 {
			t.Fatalf("channel %s last-message = %d, want 0 (activity must not be tracked)", ch, got)
		}
	}
}
