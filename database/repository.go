package database

import (
	"context"
	"strconv"

	"polynux/disgoroq/db"
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

const (
	DefaultThreshold           = 0.1
	DefaultThresholdSexe       = 0.05
	DefaultMaxTokens           = 200
	DefaultTemperature         = 0.5
	DefaultMessagesCount       = 100
	DefaultRateLimit     int64 = 10
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
