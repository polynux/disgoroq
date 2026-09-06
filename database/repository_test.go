package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/db"
)

func setupTestDB(t *testing.T) *Repository {
	return setupTestDBWithHandle(t).repo
}

func setupTestDBWithHandle(t *testing.T) struct {
	repo *Repository
	dh   *sql.DB
} {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	schemaPath := filepath.Join("..", "schema.sql")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("Failed to read schema: %v", err)
	}

	for _, statement := range strings.Split(string(schema), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if strings.Contains(statement, "libsql_vector_idx") {
			continue
		}
		if _, err = database.Exec(statement); err != nil {
			t.Fatalf("Failed to create tables: %v", err)
		}
	}

	queries := db.New(database)
	repo := NewRepositoryWithDB(database)
	repo.queries = queries

	t.Cleanup(func() {
		database.Close()
	})

	return struct {
		repo *Repository
		dh   *sql.DB
	}{repo: repo, dh: database}
}

func TestRepository_GetThreshold_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	threshold := repo.GetThreshold(ctx, "test-guild")
	if threshold != DefaultThreshold {
		t.Errorf("Expected default threshold %f, got %f", DefaultThreshold, threshold)
	}
}

func TestRepository_GetThreshold_WithValue(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "threshold", "0.7")
	if err != nil {
		t.Fatalf("Failed to set threshold: %v", err)
	}

	threshold := repo.GetThreshold(ctx, "test-guild")
	if threshold != 0.7 {
		t.Errorf("Expected threshold 0.7, got %f", threshold)
	}
}

func TestRepository_GetThresholdSexe_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	threshold := repo.GetThresholdSexe(ctx, "test-guild")
	if threshold != DefaultThresholdSexe {
		t.Errorf("Expected default thresholdSexe %f, got %f", DefaultThresholdSexe, threshold)
	}
}

func TestRepository_GetThresholdSexe_WithValue(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "thresholdSexe", "0.15")
	if err != nil {
		t.Fatalf("Failed to set thresholdSexe: %v", err)
	}

	threshold := repo.GetThresholdSexe(ctx, "test-guild")
	if threshold != 0.15 {
		t.Errorf("Expected thresholdSexe 0.15, got %f", threshold)
	}
}

func TestRepository_GetState_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	state := repo.GetState(ctx, "test-guild")
	if state != "on" {
		t.Errorf("Expected default state 'on', got '%s'", state)
	}
}

func TestRepository_GetState_WithValue(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "state", "off")
	if err != nil {
		t.Fatalf("Failed to set state: %v", err)
	}

	state := repo.GetState(ctx, "test-guild")
	if state != "off" {
		t.Errorf("Expected state 'off', got '%s'", state)
	}
}

func TestRepository_GetLastMessage_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	timestamp := repo.GetLastMessage(ctx, "test-guild")
	if timestamp != 0 {
		t.Errorf("Expected default last_message 0, got %d", timestamp)
	}
}

func TestRepository_SetAndGetLastMessage(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	expectedTimestamp := int64(1234567890)
	err := repo.SetLastMessage(ctx, "test-guild", expectedTimestamp)
	if err != nil {
		t.Fatalf("Failed to set last_message: %v", err)
	}

	timestamp := repo.GetLastMessage(ctx, "test-guild")
	if timestamp != expectedTimestamp {
		t.Errorf("Expected timestamp %d, got %d", expectedTimestamp, timestamp)
	}
}

func TestRepository_GetMessagesCount_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	count := repo.GetMessagesCount(ctx, "test-guild")
	if count != DefaultMessagesCount {
		t.Errorf("Expected default messagesCount %d, got %d", DefaultMessagesCount, count)
	}
}

func TestRepository_GetMessagesCount_WithValue(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "messagescount", "50")
	if err != nil {
		t.Fatalf("Failed to set messagescount: %v", err)
	}

	count := repo.GetMessagesCount(ctx, "test-guild")
	if count != 50 {
		t.Errorf("Expected messagesCount 50, got %d", count)
	}
}

func TestRepository_GetTemperature_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	temp := repo.GetTemperature(ctx, "test-guild")
	if temp != DefaultTemperature {
		t.Errorf("Expected default temperature %f, got %f", DefaultTemperature, temp)
	}
}

func TestRepository_GetTemperature_WithValue(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "temperature", "0.8")
	if err != nil {
		t.Fatalf("Failed to set temperature: %v", err)
	}

	temp := repo.GetTemperature(ctx, "test-guild")
	if temp != 0.8 {
		t.Errorf("Expected temperature 0.8, got %f", temp)
	}
}

func TestRepository_GetPrompt_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	prompt, ok := repo.GetPrompt(ctx, "test-guild")
	if ok {
		t.Errorf("Expected prompt not to be found, got '%s'", prompt)
	}
	if prompt != "" {
		t.Errorf("Expected empty prompt, got '%s'", prompt)
	}
}

func TestRepository_GetPrompt_WithValue(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "prompt", "custom prompt")
	if err != nil {
		t.Fatalf("Failed to set prompt: %v", err)
	}

	prompt, ok := repo.GetPrompt(ctx, "test-guild")
	if !ok {
		t.Errorf("Expected prompt to be found")
	}
	if prompt != "custom prompt" {
		t.Errorf("Expected prompt 'custom prompt', got '%s'", prompt)
	}
}

func TestRepository_TriggerWordsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetTriggerWords(ctx, "test-guild", []string{"Feun", "feunboy", "feun"})
	require.NoError(t, err)

	triggerWords, ok := repo.GetTriggerWords(ctx, "test-guild")
	require.True(t, ok)
	assert.Equal(t, []string{"feun", "feunboy"}, triggerWords)
}

func TestRepository_DeleteTriggerWords(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	require.NoError(t, repo.SetTriggerWords(ctx, "test-guild", []string{"feun"}))
	require.NoError(t, repo.DeleteTriggerWords(ctx, "test-guild"))

	_, ok := repo.GetTriggerWords(ctx, "test-guild")
	assert.False(t, ok)
}

func TestRepository_SetGuildSetting(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "test-setting", "test-value")
	if err != nil {
		t.Fatalf("Failed to set guild setting: %v", err)
	}

	value, err := repo.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "test-setting",
		GuildID: "test-guild",
	})
	if err != nil {
		t.Fatalf("Failed to get guild setting: %v", err)
	}

	if value != "test-value" {
		t.Errorf("Expected value 'test-value', got '%s'", value)
	}
}

func TestRepository_DeleteGuildSetting(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "test-setting", "test-value")
	if err != nil {
		t.Fatalf("Failed to set guild setting: %v", err)
	}

	err = repo.DeleteGuildSetting(ctx, "test-guild", "test-setting")
	if err != nil {
		t.Fatalf("Failed to delete guild setting: %v", err)
	}

	_, err = repo.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "test-setting",
		GuildID: "test-guild",
	})
	if err == nil {
		t.Errorf("Expected error when getting deleted setting, got nil")
	}
}

func TestRepository_GetAllGuilds(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "guild-1", "setting", "value1")
	if err != nil {
		t.Fatalf("Failed to set guild 1: %v", err)
	}
	err = repo.SetGuildSetting(ctx, "guild-2", "setting", "value2")
	if err != nil {
		t.Fatalf("Failed to set guild 2: %v", err)
	}
	err = repo.SetGuildSetting(ctx, "guild-1", "other", "value3")
	if err != nil {
		t.Fatalf("Failed to set other setting: %v", err)
	}

	guilds, err := repo.GetAllGuilds(ctx)
	if err != nil {
		t.Fatalf("Failed to get all guilds: %v", err)
	}

	if len(guilds) != 2 {
		t.Errorf("Expected 2 guilds, got %d", len(guilds))
	}

	guildMap := make(map[string]bool)
	for _, guild := range guilds {
		guildMap[guild] = true
	}

	if !guildMap["guild-1"] || !guildMap["guild-2"] {
		t.Errorf("Expected guilds guild-1 and guild-2, got %v", guilds)
	}
}

func TestRepository_GetHoroscopeChannel_NotSet(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	_, err := repo.GetHoroscopeChannel(ctx, "test-guild")
	if err == nil {
		t.Errorf("Expected error when horoscope channel not set")
	}
}

func TestRepository_GetHoroscopeChannel_Set(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "horoscope_channel", "channel-123")
	if err != nil {
		t.Fatalf("Failed to set horoscope channel: %v", err)
	}

	channelID, err := repo.GetHoroscopeChannel(ctx, "test-guild")
	if err != nil {
		t.Errorf("Failed to get horoscope channel: %v", err)
	}

	if channelID != "channel-123" {
		t.Errorf("Expected channel ID 'channel-123', got '%s'", channelID)
	}
}

func TestRepository_GetFartingFridayChannel_NotSet(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	_, err := repo.GetFartingFridayChannel(ctx, "test-guild")
	if err == nil {
		t.Errorf("Expected error when farting friday channel not set")
	}
}

func TestRepository_GetFartingFridayChannel_Set(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	err := repo.SetGuildSetting(ctx, "test-guild", "farting_friday_channel", "channel-456")
	if err != nil {
		t.Fatalf("Failed to set farting friday channel: %v", err)
	}

	channelID, err := repo.GetFartingFridayChannel(ctx, "test-guild")
	if err != nil {
		t.Errorf("Failed to get farting friday channel: %v", err)
	}

	if channelID != "channel-456" {
		t.Errorf("Expected channel ID 'channel-456', got '%s'", channelID)
	}
}

func TestRepository_GetReengageEnabled_DefaultFalse(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	enabled := repo.GetReengageEnabled(ctx, "test-guild", "channel-123")
	if enabled {
		t.Errorf("Expected reengage to default to disabled")
	}
}

func TestRepository_GetReengageChance_NotConfigured(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	chance, ok := repo.GetReengageChance(ctx, "test-guild", "channel-123")
	if ok {
		t.Errorf("Expected reengage chance to report not configured")
	}
	if chance != 0 {
		t.Errorf("Expected zero chance when not configured, got %f", chance)
	}
}

func TestRepository_GetReengageThreshold_NotConfigured(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	threshold, ok := repo.GetReengageThreshold(ctx, "test-guild", "channel-123")
	if ok {
		t.Errorf("Expected reengage threshold to report not configured")
	}
	if threshold != 0 {
		t.Errorf("Expected zero threshold when not configured, got %d", threshold)
	}
}

func TestRepository_AttachmentCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	expected := AttachmentCacheEntry{
		AttachmentCacheKey: AttachmentCacheKey{
			Kind:               "image_description",
			AttachmentKey:      "fingerprint",
			Provider:           "groq",
			Model:              "llama-4",
			InstructionVersion: "image_description:v1",
		},
		Content:     "cached content",
		SourceURL:   "https://example.com/image.png",
		Filename:    "",
		ContentType: "image/png",
		SizeBytes:   1234,
	}

	require.NoError(t, repo.PutAttachmentCache(ctx, expected))

	actual, found, err := repo.GetAttachmentCache(ctx, expected.AttachmentCacheKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, expected.Kind, actual.Kind)
	assert.Equal(t, expected.AttachmentKey, actual.AttachmentKey)
	assert.Equal(t, expected.Provider, actual.Provider)
	assert.Equal(t, expected.Model, actual.Model)
	assert.Equal(t, expected.InstructionVersion, actual.InstructionVersion)
	assert.Equal(t, expected.Content, actual.Content)
	assert.Equal(t, expected.SourceURL, actual.SourceURL)
	assert.Equal(t, expected.ContentType, actual.ContentType)
	assert.Equal(t, expected.SizeBytes, actual.SizeBytes)
	assert.NotZero(t, actual.CreatedAt)
	assert.NotZero(t, actual.UpdatedAt)
}

func TestRepository_AttachmentCacheMiss(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	entry, found, err := repo.GetAttachmentCache(ctx, AttachmentCacheKey{
		Kind:               "document_summary",
		AttachmentKey:      "missing",
		Provider:           "groq",
		Model:              "llama-4",
		InstructionVersion: "document_summary:v1",
	})

	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, AttachmentCacheEntry{}, entry)
}

func TestRepository_DiscordMessageCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)
	guildID := snowflake.MustParse("1137756996813193227")
	channelID := snowflake.MustParse("1137756996813193228")
	messageID := snowflake.MustParse("1137756996813193229")
	createdAt := time.Unix(1710000000, 0).UTC()

	message := discord.Message{
		ID:        messageID,
		GuildID:   &guildID,
		ChannelID: channelID,
		Content:   "hello",
		CreatedAt: createdAt,
		Author: discord.User{
			ID:       snowflake.MustParse("1474846449459138734"),
			Username: "alice",
		},
		Attachments: []discord.Attachment{{
			ID:          snowflake.MustParse("1137756996813193230"),
			Filename:    "cat.png",
			URL:         "https://example.com/cat.png",
			ContentType: ptr("image/png"),
			Size:        1234,
		}},
	}

	require.NoError(t, repo.CacheDiscordMessage(ctx, message))

	messages, err := repo.GetRecentDiscordMessagesByChannel(ctx, channelID.String(), 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, message.ID, messages[0].ID)
	assert.Equal(t, message.Content, messages[0].Content)
	require.Len(t, messages[0].Attachments, 1)
	assert.Equal(t, "cat.png", messages[0].Attachments[0].Filename)
}

func TestRepository_DiscordMessageDeletionTombstonesCache(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)
	guildID := snowflake.MustParse("1137756996813193227")
	channelID := snowflake.MustParse("1137756996813193228")
	messageID := snowflake.MustParse("1137756996813193229")

	require.NoError(t, repo.CacheDiscordMessage(ctx, discord.Message{
		ID:        messageID,
		GuildID:   &guildID,
		ChannelID: channelID,
		Content:   "hello",
		CreatedAt: time.Unix(1710000000, 0).UTC(),
		Author: discord.User{
			ID:       snowflake.MustParse("1474846449459138734"),
			Username: "alice",
		},
	}))
	require.NoError(t, repo.MarkDiscordMessageDeleted(ctx, messageID.String(), channelID.String(), guildID.String(), time.Now().UTC()))

	messages, err := repo.GetRecentDiscordMessagesByChannel(ctx, channelID.String(), 10)
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestRepository_DiscordMessageHistoryExhaustedState(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	assert.False(t, repo.IsDiscordMessageHistoryExhausted(ctx, "channel-1"))
	require.NoError(t, repo.SetDiscordMessageHistoryExhausted(ctx, "channel-1", "guild-1", true))
	assert.True(t, repo.IsDiscordMessageHistoryExhausted(ctx, "channel-1"))
	require.NoError(t, repo.SetDiscordMessageHistoryExhausted(ctx, "channel-1", "guild-1", false))
	assert.False(t, repo.IsDiscordMessageHistoryExhausted(ctx, "channel-1"))
}

func ptr[T any](v T) *T {
	return &v
}

func TestRepository_DeleteMessageBufferOlderThan(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	now := time.Now().Unix()
	old, recent := now-1000, now-10

	// Insert one old and one recent buffered message
	err := repo.queries.InsertMessageBuffer(ctx, db.InsertMessageBufferParams{
		GuildID:    "g1",
		ChannelID:  "c1",
		MessageID:  "old-msg",
		UserID:     "u1",
		AuthorNick: "old",
		Content:    "old content",
		HasImage:   sql.NullBool{Bool: false, Valid: true},
		Timestamp:  old,
		Processed:  sql.NullBool{Bool: false, Valid: true},
	})
	require.NoError(t, err)
	err = repo.queries.InsertMessageBuffer(ctx, db.InsertMessageBufferParams{
		GuildID:    "g1",
		ChannelID:  "c1",
		MessageID:  "recent-msg",
		UserID:     "u1",
		AuthorNick: "recent",
		Content:    "recent content",
		HasImage:   sql.NullBool{Bool: false, Valid: true},
		Timestamp:  recent,
		Processed:  sql.NullBool{Bool: false, Valid: true},
	})
	require.NoError(t, err)

	deleted, err := repo.DeleteMessageBufferOlderThan(ctx, now-500)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// The old row is gone, the recent one remains
	count, err := repo.queries.GetUnprocessedMessageCount(ctx, db.GetUnprocessedMessageCountParams{
		GuildID: "g1",
		UserID:  "u1",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestRepository_DeleteDiscordMessagesOlderThan(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	now := time.Now().Unix()

	err := repo.queries.UpsertDiscordMessage(ctx, db.UpsertDiscordMessageParams{
		MessageID:      "old-dm",
		ChannelID:      "c1",
		GuildID:        "g1",
		AuthorID:       "u1",
		AuthorUsername: "u1",
		Content:        "old",
		MessageJson:    "{}",
		CreatedAt:      now - 1000,
	})
	require.NoError(t, err)
	err = repo.queries.UpsertDiscordMessage(ctx, db.UpsertDiscordMessageParams{
		MessageID:      "recent-dm",
		ChannelID:      "c1",
		GuildID:        "g1",
		AuthorID:       "u1",
		AuthorUsername: "u1",
		Content:        "recent",
		MessageJson:    "{}",
		CreatedAt:      now - 10,
	})
	require.NoError(t, err)

	deleted, err := repo.DeleteDiscordMessagesOlderThan(ctx, now-500)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Verify only the recent row remains
	rows, err := repo.queries.GetRecentDiscordMessagesByChannel(ctx, db.GetRecentDiscordMessagesByChannelParams{
		ChannelID: "c1",
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "recent-dm", rows[0].MessageID)
}

func TestRepository_DeleteAttachmentCacheOlderThan(t *testing.T) {
	ctx := context.Background()
	setup := setupTestDBWithHandle(t)
	repo, dh := setup.repo, setup.dh

	now := time.Now().Unix()

	err := repo.queries.UpsertAttachmentCacheEntry(ctx, db.UpsertAttachmentCacheEntryParams{
		CacheKind:          "image",
		AttachmentKey:      "old-key",
		SourceUrl:          "https://example.com/old",
		Filename:           "old.png",
		ContentType:        "image/png",
		SizeBytes:          10,
		Provider:           "groq",
		Model:              "m",
		InstructionVersion: "v1",
		Content:            "old",
	})
	require.NoError(t, err)
	err = repo.queries.UpsertAttachmentCacheEntry(ctx, db.UpsertAttachmentCacheEntryParams{
		CacheKind:          "image",
		AttachmentKey:      "recent-key",
		SourceUrl:          "https://example.com/recent",
		Filename:           "recent.png",
		ContentType:        "image/png",
		SizeBytes:          10,
		Provider:           "groq",
		Model:              "m",
		InstructionVersion: "v1",
		Content:            "recent",
	})
	require.NoError(t, err)

	// Backdate the old row's created_at (Upsert uses strftime('%s','now'))
	if _, err := dh.Exec("UPDATE attachment_cache SET created_at = ? WHERE attachment_key = 'old-key'", now-1000); err != nil {
		require.NoError(t, err)
	}

	deleted, err := repo.DeleteAttachmentCacheOlderThan(ctx, now-500)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

func TestRepository_MemoryEnabled_Default(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	// Default is enabled when the key is absent
	if !repo.GetMemoryEnabled(ctx, "guild-x") {
		t.Error("expected memory to default to enabled")
	}
}

func TestRepository_MemoryEnabled_RoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := setupTestDB(t)

	if err := repo.SetMemoryEnabled(ctx, "guild-y", false); err != nil {
		t.Fatalf("SetMemoryEnabled: %v", err)
	}
	if repo.GetMemoryEnabled(ctx, "guild-y") {
		t.Error("expected memory to be disabled after SetMemoryEnabled(false)")
	}

	if err := repo.SetMemoryEnabled(ctx, "guild-y", true); err != nil {
		t.Fatalf("SetMemoryEnabled: %v", err)
	}
	if !repo.GetMemoryEnabled(ctx, "guild-y") {
		t.Error("expected memory to be enabled after SetMemoryEnabled(true)")
	}

	// Other guilds unaffected
	if !repo.GetMemoryEnabled(ctx, "guild-z") {
		t.Error("expected other guilds to keep default enabled")
	}
}
