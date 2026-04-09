package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/db"
)

func setupTestDB(t *testing.T) *Repository {
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

	_, err = database.Exec(string(schema))
	if err != nil {
		t.Fatalf("Failed to create tables: %v", err)
	}

	queries := db.New(database)
	repo := NewRepository()

	repo.queries = queries

	t.Cleanup(func() {
		database.Close()
	})

	return repo
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
