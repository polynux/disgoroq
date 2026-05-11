package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"

	"polynux/disgoroq/db"
)

func (r *Repository) CacheDiscordMessage(ctx context.Context, message discord.Message) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal discord message: %w", err)
	}

	guildID := ""
	if message.GuildID != nil {
		guildID = message.GuildID.String()
	}

	referencedMessageID := ""
	if message.MessageReference != nil && message.MessageReference.MessageID != nil {
		referencedMessageID = message.MessageReference.MessageID.String()
	}

	return r.queries.UpsertDiscordMessage(ctx, db.UpsertDiscordMessageParams{
		MessageID:           message.ID.String(),
		ChannelID:           message.ChannelID.String(),
		GuildID:             guildID,
		AuthorID:            message.Author.ID.String(),
		AuthorUsername:      message.Author.Username,
		Content:             message.Content,
		ReferencedMessageID: referencedMessageID,
		MessageJson:         string(payload),
		CreatedAt:           messageCreatedAt(message),
		EditedAt:            nullableUnix(message.EditedTimestamp),
		DeletedAt:           sql.NullInt64{},
	})
}

func (r *Repository) CacheDiscordMessages(ctx context.Context, messages []discord.Message) error {
	for _, message := range messages {
		if err := r.CacheDiscordMessage(ctx, message); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) MarkDiscordMessageDeleted(ctx context.Context, messageID, channelID, guildID string, deletedAt time.Time) error {
	return r.queries.MarkDiscordMessageDeleted(ctx, db.MarkDiscordMessageDeletedParams{
		MessageID: messageID,
		ChannelID: channelID,
		GuildID:   guildID,
		CreatedAt: deletedAt.Unix(),
		DeletedAt: sql.NullInt64{
			Int64: deletedAt.Unix(),
			Valid: true,
		},
	})
}

func (r *Repository) GetRecentDiscordMessagesByChannel(ctx context.Context, channelID string, limit int) ([]discord.Message, error) {
	rows, err := r.queries.GetRecentDiscordMessagesByChannel(ctx, db.GetRecentDiscordMessagesByChannelParams{
		ChannelID: channelID,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}

	messages := make([]discord.Message, 0, len(rows))
	for _, row := range rows {
		var message discord.Message
		if err := json.Unmarshal([]byte(row.MessageJson), &message); err != nil {
			return nil, fmt.Errorf("unmarshal cached discord message %s: %w", row.MessageID, err)
		}
		messages = append(messages, message)
	}

	return messages, nil
}

func (r *Repository) IsDiscordMessageHistoryExhausted(ctx context.Context, channelID string) bool {
	exhausted, err := r.queries.GetDiscordMessageCacheState(ctx, channelID)
	if err != nil {
		return false
	}
	return exhausted
}

func (r *Repository) SetDiscordMessageHistoryExhausted(ctx context.Context, channelID, guildID string, exhausted bool) error {
	return r.queries.UpsertDiscordMessageCacheState(ctx, db.UpsertDiscordMessageCacheStateParams{
		ChannelID:        channelID,
		GuildID:          guildID,
		HistoryExhausted: exhausted,
	})
}

func messageCreatedAt(message discord.Message) int64 {
	if !message.CreatedAt.IsZero() {
		return message.CreatedAt.Unix()
	}
	if message.ID != 0 {
		return message.ID.Time().Unix()
	}
	return time.Now().Unix()
}

func nullableUnix(timestamp *time.Time) sql.NullInt64 {
	if timestamp == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{
		Int64: timestamp.Unix(),
		Valid: true,
	}
}
