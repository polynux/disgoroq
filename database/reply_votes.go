package database

import (
	"context"

	"polynux/disgoroq/db"
)

type BotReplyVote struct {
	MessageID        string
	GuildID          string
	ChannelID        string
	Content          string
	ReplyToMessageID string
	ReplyToContent   string
	Provider         string
	Model            string
	GoodCount        int64
	BadCount         int64
	CreatedAt        int64
	UpdatedAt        int64
}

func (r *Repository) InsertBotReplyVote(ctx context.Context, vote BotReplyVote) error {
	return r.queries.InsertBotReplyVote(ctx, db.InsertBotReplyVoteParams{
		MessageID:        vote.MessageID,
		GuildID:          vote.GuildID,
		ChannelID:        vote.ChannelID,
		Content:          vote.Content,
		ReplyToMessageID: vote.ReplyToMessageID,
		ReplyToContent:   vote.ReplyToContent,
		Provider:         vote.Provider,
		Model:            vote.Model,
	})
}

func (r *Repository) GetBotReplyVote(ctx context.Context, messageID string) (BotReplyVote, error) {
	row, err := r.queries.GetBotReplyVote(ctx, messageID)
	if err != nil {
		return BotReplyVote{}, err
	}
	return BotReplyVote{
		MessageID: row.MessageID, GuildID: row.GuildID, ChannelID: row.ChannelID,
		Content: row.Content, ReplyToMessageID: row.ReplyToMessageID,
		ReplyToContent: row.ReplyToContent, Provider: row.Provider, Model: row.Model,
		GoodCount: row.GoodCount, BadCount: row.BadCount,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (r *Repository) UpdateBotReplyVoteCounts(ctx context.Context, messageID string, goodCount, badCount int) error {
	return r.queries.UpdateBotReplyVoteCounts(ctx, db.UpdateBotReplyVoteCountsParams{
		GoodCount: int64(goodCount), BadCount: int64(badCount), MessageID: messageID,
	})
}
