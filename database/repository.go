package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"polynux/disgoroq/db"
	"polynux/disgoroq/triggerwords"
	"polynux/disgoroq/utils"
)

type Repository struct {
	queries *db.Queries
}

func NewRepository() *Repository {
	return &Repository{
		queries: utils.Q,
	}
}

func NewRepositoryWithDB(database *sql.DB) *Repository {
	return &Repository{
		queries: db.New(database),
	}
}

func (r *Repository) GetDB() *sql.DB {
	return utils.GetDB()
}

const (
	DefaultThreshold               = 0.1
	DefaultThresholdSexe           = 0.05
	DefaultMaxTokens               = 200
	DefaultTemperature             = 0.5
	DefaultMessagesCount           = 100
	DefaultRateLimit         int64 = 10
	DefaultReengageChance          = 0.01
	DefaultReengageThreshold       = 30
)

func (r *Repository) GetThreshold(ctx context.Context, guildID string) float64 {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "threshold",
		GuildID: guildID,
	})
	if err != nil {
		return DefaultThreshold
	}
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return DefaultThreshold
	}
	return threshold
}

func (r *Repository) GetThresholdSexe(ctx context.Context, guildID string) float64 {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "thresholdSexe",
		GuildID: guildID,
	})
	if err != nil {
		return DefaultThresholdSexe
	}
	thresholdSexe, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return DefaultThresholdSexe
	}
	return thresholdSexe
}

func (r *Repository) GetState(ctx context.Context, guildID string) string {
	state, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "state",
		GuildID: guildID,
	})
	if err != nil {
		return "on"
	}
	return state
}

func (r *Repository) GetLastMessage(ctx context.Context, guildID string) int64 {
	lastMessage, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "last_message",
		GuildID: guildID,
	})
	if err != nil {
		return 0
	}
	timestamp, err := strconv.ParseInt(lastMessage, 10, 64)
	if err != nil {
		return 0
	}
	return timestamp
}

func (r *Repository) SetLastMessage(ctx context.Context, guildID string, timestamp int64) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "last_message",
		Value:   strconv.FormatInt(timestamp, 10),
	})
}

func (r *Repository) GetMessagesCount(ctx context.Context, guildID string) int {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "messagescount",
		GuildID: guildID,
	})
	if err != nil {
		return DefaultMessagesCount
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return DefaultMessagesCount
	}
	return int(count)
}

func (r *Repository) GetTemperature(ctx context.Context, guildID string) float32 {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "temperature",
		GuildID: guildID,
	})
	if err != nil {
		return DefaultTemperature
	}
	temp, err := strconv.ParseFloat(value, 32)
	if err != nil {
		return DefaultTemperature
	}
	return float32(temp)
}

func (r *Repository) GetPrompt(ctx context.Context, guildID string) (string, bool) {
	prompt, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "prompt",
		GuildID: guildID,
	})
	if err != nil {
		return "", false
	}
	return prompt, true
}

func (r *Repository) GetVoicePrompt(ctx context.Context, guildID string) (string, bool) {
	prompt, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "voice_prompt",
		GuildID: guildID,
	})
	if err != nil {
		return "", false
	}
	return prompt, true
}

func (r *Repository) GetTriggerWords(ctx context.Context, guildID string) ([]string, bool) {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "trigger_words",
		GuildID: guildID,
	})
	if err != nil {
		return nil, false
	}

	var words []string
	if err := json.Unmarshal([]byte(value), &words); err != nil {
		return nil, false
	}

	return triggerwords.NormalizeAll(words), true
}

func (r *Repository) SetTriggerWords(ctx context.Context, guildID string, words []string) error {
	normalized := triggerwords.NormalizeAll(words)
	if err := triggerwords.Validate(normalized); err != nil {
		return err
	}

	payload, err := json.Marshal(normalized)
	if err != nil {
		return err
	}

	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "trigger_words",
		Value:   string(payload),
	})
}

func (r *Repository) DeleteTriggerWords(ctx context.Context, guildID string) error {
	return r.queries.DeleteGuildSetting(ctx, db.DeleteGuildSettingParams{
		GuildID: guildID,
		Name:    "trigger_words",
	})
}

func (r *Repository) SetGuildSetting(ctx context.Context, guildID, name, value string) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    name,
		Value:   value,
	})
}

func (r *Repository) DeleteGuildSetting(ctx context.Context, guildID, name string) error {
	return r.queries.DeleteGuildSetting(ctx, db.DeleteGuildSettingParams{
		GuildID: guildID,
		Name:    name,
	})
}

func (r *Repository) GetAllGuilds(ctx context.Context) ([]string, error) {
	return r.queries.GetAllGuilds(ctx)
}

func (r *Repository) GetHoroscopeChannel(ctx context.Context, guildID string) (string, error) {
	return r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "horoscope_channel",
		GuildID: guildID,
	})
}

func (r *Repository) GetFartingFridayChannel(ctx context.Context, guildID string) (string, error) {
	return r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "farting_friday_channel",
		GuildID: guildID,
	})
}

func (r *Repository) SetChannelLastMessage(ctx context.Context, guildID, channelID string, timestamp int64) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "channel_last_message:" + channelID,
		Value:   strconv.FormatInt(timestamp, 10),
	})
}

func (r *Repository) GetChannelLastMessage(ctx context.Context, guildID, channelID string) int64 {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "channel_last_message:" + channelID,
		GuildID: guildID,
	})
	if err != nil {
		return 0
	}
	timestamp, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return timestamp
}

func (r *Repository) SetReengageEnabled(ctx context.Context, guildID, channelID string, enabled bool) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_enabled:" + channelID,
		Value:   strconv.FormatBool(enabled),
	})
}

func (r *Repository) GetReengageEnabled(ctx context.Context, guildID, channelID string) bool {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "reengage_enabled:" + channelID,
		GuildID: guildID,
	})
	if err != nil {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return enabled
}

func (r *Repository) SetReengageChance(ctx context.Context, guildID, channelID string, chance float64) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_chance:" + channelID,
		Value:   strconv.FormatFloat(chance, 'f', -1, 64),
	})
}

func (r *Repository) GetReengageChance(ctx context.Context, guildID, channelID string) (float64, bool) {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "reengage_chance:" + channelID,
		GuildID: guildID,
	})
	if err != nil {
		return 0, false
	}
	chance, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return chance, true
}

func (r *Repository) SetReengageThreshold(ctx context.Context, guildID, channelID string, minutes int) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_threshold:" + channelID,
		Value:   strconv.Itoa(minutes),
	})
}

func (r *Repository) GetReengageThreshold(ctx context.Context, guildID, channelID string) (int, bool) {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "reengage_threshold:" + channelID,
		GuildID: guildID,
	})
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return minutes, true
}

func (r *Repository) DeleteReengageConfig(ctx context.Context, guildID, channelID string) error {
	if err := r.queries.DeleteGuildSetting(ctx, db.DeleteGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_enabled:" + channelID,
	}); err != nil {
		return err
	}
	r.queries.DeleteGuildSetting(ctx, db.DeleteGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_chance:" + channelID,
	})
	r.queries.DeleteGuildSetting(ctx, db.DeleteGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_threshold:" + channelID,
	})
	return nil
}

func (r *Repository) GetAllReengageChannels(ctx context.Context, guildID string) ([]string, error) {
	rows, err := r.GetDB().QueryContext(ctx,
		"SELECT name, value FROM guild_settings WHERE guild_id = ?",
		guildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	channelMap := make(map[string]bool)
	prefix := "reengage_enabled:"
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		if strings.HasPrefix(name, prefix) {
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				continue
			}
			channelID := strings.TrimPrefix(name, prefix)
			channelMap[channelID] = enabled
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var channels []string
	for channelID, enabled := range channelMap {
		if enabled {
			channels = append(channels, channelID)
		}
	}
	return channels, nil
}

func (r *Repository) SetReengageMessage(ctx context.Context, guildID, message string) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "reengage_message",
		Value:   message,
	})
}

func (r *Repository) GetReengageMessage(ctx context.Context, guildID string) (string, bool) {
	message, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "reengage_message",
		GuildID: guildID,
	})
	if err != nil {
		return "", false
	}
	return message, true
}

// Voice settings methods

// GetVoiceEnabled returns whether voice is enabled for a guild.
func (r *Repository) GetVoiceEnabled(ctx context.Context, guildID string) bool {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "voice_enabled",
		GuildID: guildID,
	})
	if err != nil {
		return true // Default to enabled
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return true
	}
	return enabled
}

// SetVoiceEnabled sets whether voice is enabled for a guild.
func (r *Repository) SetVoiceEnabled(ctx context.Context, guildID string, enabled bool) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "voice_enabled",
		Value:   strconv.FormatBool(enabled),
	})
}

// GetVoiceAutoJoin returns whether auto-join is enabled for a guild.
func (r *Repository) GetVoiceAutoJoin(ctx context.Context, guildID string) bool {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "voice_auto_join",
		GuildID: guildID,
	})
	if err != nil {
		return false // Default to disabled
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return enabled
}

// SetVoiceAutoJoin sets whether auto-join is enabled for a guild.
func (r *Repository) SetVoiceAutoJoin(ctx context.Context, guildID string, enabled bool) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "voice_auto_join",
		Value:   strconv.FormatBool(enabled),
	})
}

// GetVoiceAutoJoinChannel returns the auto-join channel for a guild.
func (r *Repository) GetVoiceAutoJoinChannel(ctx context.Context, guildID string) (string, bool) {
	channelID, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "voice_auto_join_channel",
		GuildID: guildID,
	})
	if err != nil {
		return "", false
	}
	return channelID, true
}

// SetVoiceAutoJoinChannel sets the auto-join channel for a guild.
func (r *Repository) SetVoiceAutoJoinChannel(ctx context.Context, guildID, channelID string) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "voice_auto_join_channel",
		Value:   channelID,
	})
}

// GetVoiceConfig returns all voice settings for a guild.
func (r *Repository) GetVoiceConfig(ctx context.Context, guildID string) (enabled, autoJoin bool, autoJoinChannel string) {
	enabled = r.GetVoiceEnabled(ctx, guildID)
	autoJoin = r.GetVoiceAutoJoin(ctx, guildID)
	autoJoinChannel, _ = r.GetVoiceAutoJoinChannel(ctx, guildID)
	return
}

// DeleteVoiceConfig removes all voice settings for a guild.
func (r *Repository) DeleteVoiceConfig(ctx context.Context, guildID string) error {
	settings := []string{"voice_enabled", "voice_auto_join", "voice_auto_join_channel"}
	for _, setting := range settings {
		if err := r.queries.DeleteGuildSetting(ctx, db.DeleteGuildSettingParams{
			GuildID: guildID,
			Name:    setting,
		}); err != nil {
			// Ignore "not found" errors
		}
	}
	return nil
}

// DeleteMessageBufferOlderThan removes buffered messages older than the
// given cutoff timestamp (unix seconds) and returns the number of rows deleted.
func (r *Repository) DeleteMessageBufferOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	return r.queries.DeleteMessageBufferOlderThan(ctx, cutoff)
}

// DeleteDiscordMessagesOlderThan removes cached Discord messages older than
// the given cutoff timestamp (unix seconds) and returns the number of rows deleted.
func (r *Repository) DeleteDiscordMessagesOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	return r.queries.DeleteDiscordMessagesOlderThan(ctx, cutoff)
}

// DeleteAttachmentCacheOlderThan removes attachment cache entries older than
// the given cutoff timestamp (unix seconds) and returns the number of rows deleted.
func (r *Repository) DeleteAttachmentCacheOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	return r.queries.DeleteAttachmentCacheOlderThan(ctx, cutoff)
}

// GetMemoryEnabled returns whether conversation memory is enabled for a guild.
// Defaults to true when the setting is absent.
func (r *Repository) GetMemoryEnabled(ctx context.Context, guildID string) bool {
	value, err := r.queries.GetGuildSetting(ctx, db.GetGuildSettingParams{
		Name:    "memory_enabled",
		GuildID: guildID,
	})
	if err != nil {
		return true // Default to enabled
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return true
	}
	return enabled
}

// SetMemoryEnabled sets whether conversation memory is enabled for a guild.
func (r *Repository) SetMemoryEnabled(ctx context.Context, guildID string, enabled bool) error {
	return r.queries.SetGuildSetting(ctx, db.SetGuildSettingParams{
		GuildID: guildID,
		Name:    "memory_enabled",
		Value:   strconv.FormatBool(enabled),
	})
}
