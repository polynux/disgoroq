package database

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListMessageBufferUnprocessedIncludesNullProcessed(t *testing.T) {
	h := setupTestDBWithHandle(t)
	ctx := context.Background()
	for i, processed := range []any{nil, 0, 1} {
		_, err := h.dh.Exec(`INSERT INTO message_buffer (guild_id, channel_id, message_id, user_id, author_nick, content, timestamp, processed)
			VALUES ('g', 'c', ?, 'u', 'nick', 'hi', ?, ?)`, "m"+string(rune('0'+i)), 100+i, processed)
		if err != nil {
			t.Fatal(err)
		}
	}

	unprocessed, _, err := h.repo.ListMessageBuffer(ctx, "g", "", "", 0, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unprocessed) != 2 {
		t.Fatalf("unprocessed filter returned %d rows, want 2 (NULL and 0)", len(unprocessed))
	}
	processed, _, err := h.repo.ListMessageBuffer(ctx, "g", "", "", 1, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(processed) != 1 {
		t.Fatalf("processed filter returned %d rows, want 1", len(processed))
	}
}

// TestSchemaDropsSupersededDiscordMessagesIndex replays schema.sql over a DB
// that still has the old 3-column index, as an existing deployment does.
func TestSchemaDropsSupersededDiscordMessagesIndex(t *testing.T) {
	h := setupTestDBWithHandle(t)
	if _, err := h.dh.Exec(`CREATE INDEX idx_discord_messages_guild_channel_created ON discord_messages(guild_id, channel_id, created_at DESC)`); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("..", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range strings.Split(string(schema), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.Contains(stmt, "libsql_vector_idx") {
			continue
		}
		if _, err := h.dh.Exec(stmt); err != nil {
			t.Fatalf("replaying schema: %v\n%s", err, stmt)
		}
	}
	var n int
	if err := h.dh.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_discord_messages_guild_channel_created'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("superseded index still present after schema replay")
	}
	if err := h.dh.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_discord_messages_guild_channel_created_id'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("replacement index missing")
	}
}
