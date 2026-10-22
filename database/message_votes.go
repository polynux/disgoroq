package database

import (
	"context"

	"polynux/disgoroq/db"
)

type MessageVote struct {
	MessageID        string
	GuildID          string
	ChannelID        string
	AuthorID         string
	AuthorName       string
	IsBotReply       int64
	Content          string
	ReplyToMessageID string
	ReplyToContent   string
	Provider         string
	Model            string
	GoodCount        int64
	BadCount         int64
	MessageCreatedAt int64
	CreatedAt        int64
	UpdatedAt        int64
}

func (r *Repository) InsertMessageVote(ctx context.Context, vote MessageVote) error {
	return r.queries.InsertMessageVote(ctx, db.InsertMessageVoteParams{
		MessageID: vote.MessageID, GuildID: vote.GuildID, ChannelID: vote.ChannelID,
		AuthorID: vote.AuthorID, AuthorName: vote.AuthorName, IsBotReply: vote.IsBotReply,
		Content: vote.Content, ReplyToMessageID: vote.ReplyToMessageID, ReplyToContent: vote.ReplyToContent,
		Provider: vote.Provider, Model: vote.Model, MessageCreatedAt: vote.MessageCreatedAt,
	})
}

func (r *Repository) GetMessageVote(ctx context.Context, messageID string) (MessageVote, error) {
	row, err := r.queries.GetMessageVote(ctx, messageID)
	if err != nil {
		return MessageVote{}, err
	}
	return MessageVote{
		MessageID: row.MessageID, GuildID: row.GuildID, ChannelID: row.ChannelID,
		AuthorID: row.AuthorID, AuthorName: row.AuthorName, IsBotReply: row.IsBotReply,
		Content: row.Content, ReplyToMessageID: row.ReplyToMessageID, ReplyToContent: row.ReplyToContent,
		Provider: row.Provider, Model: row.Model, GoodCount: row.GoodCount, BadCount: row.BadCount,
		MessageCreatedAt: row.MessageCreatedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (r *Repository) UpdateMessageVoteCounts(ctx context.Context, messageID string, goodCount, badCount int) error {
	return r.queries.UpdateMessageVoteCounts(ctx, db.UpdateMessageVoteCountsParams{
		GoodCount: int64(goodCount), BadCount: int64(badCount), MessageID: messageID,
	})
}
