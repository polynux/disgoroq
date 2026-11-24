package database

import (
	"context"
	"encoding/json"
	"time"

	"polynux/disgoroq/db"
)

// Read-only data views backing the web admin /data pages. The row types below
// deliberately exclude embedding blobs so they never reach the browser.

// SummaryView is a conversation_summaries row with a best-effort author name.
type SummaryView struct {
	ID             int64
	GuildID        string
	UserID         string
	AuthorName     string
	SummaryText    string
	MessageCount   int64
	StartMessageID string
	EndMessageID   string
	CreatedAt      int64
	UpdatedAt      int64
}

// BufferMessageView is a message_buffer row.
type BufferMessageView struct {
	ID               int64
	GuildID          string
	ChannelID        string
	MessageID        string
	UserID           string
	AuthorNick       string
	Content          string
	HasImage         bool
	ImageDescription string
	Timestamp        int64
	Processed        bool
}

// DiscordMessageView is a discord_messages row (message_json included).
type DiscordMessageView struct {
	MessageID           string
	ChannelID           string
	GuildID             string
	AuthorID            string
	AuthorUsername      string
	Content             string
	ReferencedMessageID string
	MessageJSON         string
	CreatedAt           int64
	EditedAt            int64
	DeletedAt           int64
}

// AttachmentCacheView is an attachment_cache row.
type AttachmentCacheView struct {
	ID                 int64
	CacheKind          string
	AttachmentKey      string
	SourceURL          string
	Filename           string
	ContentType        string
	SizeBytes          int64
	Provider           string
	Model              string
	InstructionVersion string
	Content            string
	CreatedAt          int64
	UpdatedAt          int64
}

// EventListFilter narrows bot_events list queries.
type EventListFilter struct {
	From      int64
	To        int64
	EventType string
	GuildID   string
	HasError  bool
}

func (r *Repository) ListConversationSummaries(ctx context.Context, guildID, userID string, limit, offset int) ([]SummaryView, bool, error) {
	rows, err := r.queries.ListConversationSummaries(ctx, db.ListConversationSummariesParams{
		GuildID:   guildID,
		UserID:    userID,
		RowLimit:  int64(limit + 1),
		RowOffset: int64(offset),
	})
	if err != nil {
		return nil, false, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]SummaryView, len(rows))
	for i, row := range rows {
		out[i] = SummaryView{
			ID:             row.ID,
			GuildID:        row.GuildID,
			UserID:         row.UserID,
			AuthorName:     row.AuthorName,
			SummaryText:    row.SummaryText,
			MessageCount:   row.MessageCount,
			StartMessageID: row.StartMessageID.String,
			EndMessageID:   row.EndMessageID.String,
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
		}
	}
	return out, hasMore, nil
}

// DeleteConversationSummary removes one summary and reports how many rows were deleted.
func (r *Repository) DeleteConversationSummary(ctx context.Context, id int64) (int64, error) {
	return r.queries.DeleteConversationSummaryByID(ctx, id)
}

// DeleteConversationSummariesForUser is the same SQL as memory.ClearUserMemory,
// with a row count so the admin UI can report what happened.
func (r *Repository) DeleteConversationSummariesForUser(ctx context.Context, guildID, userID string) (int64, error) {
	return r.queries.DeleteConversationSummariesForUser(ctx, db.DeleteConversationSummariesForUserParams{
		GuildID: guildID,
		UserID:  userID,
	})
}

// ListMessageBuffer returns buffered messages, newest first. processed is
// -1 (all), 0 (unprocessed) or 1 (processed).
func (r *Repository) ListMessageBuffer(ctx context.Context, guildID, channelID, userID string, processed int, limit, offset int) ([]BufferMessageView, bool, error) {
	rows, err := r.queries.ListMessageBuffer(ctx, db.ListMessageBufferParams{
		GuildID:         guildID,
		ChannelID:       channelID,
		UserID:          userID,
		ProcessedFilter: int64(processed),
		RowLimit:        int64(limit + 1),
		RowOffset:       int64(offset),
	})
	if err != nil {
		return nil, false, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]BufferMessageView, len(rows))
	for i, row := range rows {
		out[i] = BufferMessageView{
			ID:               row.ID,
			GuildID:          row.GuildID,
			ChannelID:        row.ChannelID,
			MessageID:        row.MessageID,
			UserID:           row.UserID,
			AuthorNick:       row.AuthorNick,
			Content:          row.Content,
			HasImage:         row.HasImage.Bool,
			ImageDescription: row.ImageDescription.String,
			Timestamp:        row.Timestamp,
			Processed:        row.Processed.Bool,
		}
	}
	return out, hasMore, nil
}

// ListDiscordMessages returns cached Discord messages, newest first.
func (r *Repository) ListDiscordMessages(ctx context.Context, guildID, channelID, authorID, text string, includeDeleted bool, limit, offset int) ([]DiscordMessageView, bool, error) {
	includeDeletedArg := int64(0)
	if includeDeleted {
		includeDeletedArg = 1
	}

	rows, err := r.queries.ListDiscordMessages(ctx, db.ListDiscordMessagesParams{
		GuildID:        guildID,
		ChannelID:      channelID,
		AuthorID:       authorID,
		ContentFilter:  text,
		IncludeDeleted: includeDeletedArg,
		RowLimit:       int64(limit + 1),
		RowOffset:      int64(offset),
	})
	if err != nil {
		return nil, false, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]DiscordMessageView, len(rows))
	for i, row := range rows {
		out[i] = DiscordMessageView{
			MessageID:           row.MessageID,
			ChannelID:           row.ChannelID,
			GuildID:             row.GuildID,
			AuthorID:            row.AuthorID,
			AuthorUsername:      row.AuthorUsername,
			Content:             row.Content,
			ReferencedMessageID: row.ReferencedMessageID,
			MessageJSON:         row.MessageJson,
			CreatedAt:           row.CreatedAt,
			EditedAt:            row.EditedAt.Int64,
			DeletedAt:           row.DeletedAt.Int64,
		}
	}
	return out, hasMore, nil
}

// ListAttachmentCache returns cached attachment results, newest first.
func (r *Repository) ListAttachmentCache(ctx context.Context, kind, provider, model string, limit, offset int) ([]AttachmentCacheView, bool, error) {
	rows, err := r.queries.ListAttachmentCache(ctx, db.ListAttachmentCacheParams{
		CacheKind: kind,
		Provider:  provider,
		Model:     model,
		RowLimit:  int64(limit + 1),
		RowOffset: int64(offset),
	})
	if err != nil {
		return nil, false, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]AttachmentCacheView, len(rows))
	for i, row := range rows {
		out[i] = AttachmentCacheView{
			ID:                 row.ID,
			CacheKind:          row.CacheKind,
			AttachmentKey:      row.AttachmentKey,
			SourceURL:          row.SourceUrl,
			Filename:           row.Filename,
			ContentType:        row.ContentType,
			SizeBytes:          row.SizeBytes,
			Provider:           row.Provider,
			Model:              row.Model,
			InstructionVersion: row.InstructionVersion,
			Content:            row.Content,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		}
	}
	return out, hasMore, nil
}

// DeleteAttachmentCacheByID removes one attachment cache row.
func (r *Repository) DeleteAttachmentCacheByID(ctx context.Context, id int64) (int64, error) {
	return r.queries.DeleteAttachmentCacheByID(ctx, id)
}

// ListMessageVotes returns votes ordered by sort: newest, good, or bad.
func (r *Repository) ListMessageVotes(ctx context.Context, guildID string, botOnly bool, sort string, limit, offset int) ([]MessageVote, bool, error) {
	botOnlyArg := int64(0)
	if botOnly {
		botOnlyArg = 1
	}

	var (
		rows []db.MessageVote
		err  error
	)
	params := db.ListMessageVotesNewestParams{
		GuildID:   guildID,
		BotOnly:   botOnlyArg,
		RowLimit:  int64(limit + 1),
		RowOffset: int64(offset),
	}
	switch sort {
	case "good":
		rows, err = r.queries.ListMessageVotesBest(ctx, db.ListMessageVotesBestParams(params))
	case "bad":
		rows, err = r.queries.ListMessageVotesWorst(ctx, db.ListMessageVotesWorstParams(params))
	default:
		rows, err = r.queries.ListMessageVotesNewest(ctx, params)
	}
	if err != nil {
		return nil, false, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]MessageVote, len(rows))
	for i, row := range rows {
		out[i] = MessageVote{
			MessageID:        row.MessageID,
			GuildID:          row.GuildID,
			ChannelID:        row.ChannelID,
			AuthorID:         row.AuthorID,
			AuthorName:       row.AuthorName,
			IsBotReply:       row.IsBotReply,
			Content:          row.Content,
			ReplyToMessageID: row.ReplyToMessageID,
			ReplyToContent:   row.ReplyToContent,
			Provider:         row.Provider,
			Model:            row.Model,
			GoodCount:        row.GoodCount,
			BadCount:         row.BadCount,
			MessageCreatedAt: row.MessageCreatedAt,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
		}
	}
	return out, hasMore, nil
}

// ListBotEvents returns events inside the filter's time range, newest first.
// The timestamp range keeps the query on idx_bot_events_timestamp and the
// LIMIT/OFFSET bound is always applied.
func (r *Repository) ListBotEvents(ctx context.Context, filter EventListFilter, limit, offset int) ([]BotEvent, bool, error) {
	hasError := int64(0)
	if filter.HasError {
		hasError = 1
	}

	rows, err := r.queries.ListBotEvents(ctx, db.ListBotEventsParams{
		TsFrom:    filter.From,
		TsTo:      filter.To,
		EventType: filter.EventType,
		GuildID:   filter.GuildID,
		HasError:  hasError,
		RowLimit:  int64(limit + 1),
		RowOffset: int64(offset),
	})
	if err != nil {
		return nil, false, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := make([]BotEvent, len(rows))
	for i, row := range rows {
		event := BotEvent{
			Timestamp:  time.Unix(row.Timestamp, 0),
			EventType:  EventType(row.EventType),
			GuildID:    row.GuildID.String,
			ChannelID:  row.ChannelID.String,
			MessageID:  row.MessageID.String,
			UserID:     row.UserID.String,
			DurationMS: row.DurationMs.Int64,
			Error:      row.Error.String,
		}
		if row.Details.Valid && row.Details.String != "" {
			var details EventDetails
			if err := json.Unmarshal([]byte(row.Details.String), &details); err == nil {
				event.Details = &details
			}
		}
		out[i] = event
	}
	return out, hasMore, nil
}

// ListBotEventTypes returns the distinct event types present in bot_events.
func (r *Repository) ListBotEventTypes(ctx context.Context) ([]string, error) {
	return r.queries.ListBotEventTypes(ctx)
}
