package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"polynux/disgoroq/db"
)

// Read-only data views backing the web admin /data pages. List queries are
// hand-built here so that only supplied filters become predicates and SQLite
// can use the table indexes. The row types below deliberately exclude
// embedding blobs so they never reach the browser.

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

// whereBuilder accumulates optional predicates and their arguments.
type whereBuilder struct {
	clauses []string
	args    []any
}

func (b *whereBuilder) add(clause string, args ...any) {
	b.clauses = append(b.clauses, clause)
	b.args = append(b.args, args...)
}

func (b *whereBuilder) clause() string {
	if len(b.clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(b.clauses, " AND ")
}

// queryArgs returns the predicate arguments followed by extra (LIMIT/OFFSET).
func (b *whereBuilder) queryArgs(extra ...any) []any {
	args := make([]any, 0, len(b.args)+len(extra))
	args = append(args, b.args...)
	args = append(args, extra...)
	return args
}

// countRows counts a table with the same predicates as its list query.
// table is always an internal constant, never user input.
func (r *Repository) countRows(ctx context.Context, table string, where whereBuilder) (int64, error) {
	var total int64
	err := r.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+where.clause(), where.args...).Scan(&total)
	return total, err
}

func conversationSummariesWhere(guildID, userID string) whereBuilder {
	var where whereBuilder
	if guildID != "" {
		where.add("cs.guild_id = ?", guildID)
	}
	if userID != "" {
		where.add("cs.user_id = ?", userID)
	}
	return where
}

// CountConversationSummaries returns the number of rows the summaries page
// would paginate over with the same filters.
func (r *Repository) CountConversationSummaries(ctx context.Context, guildID, userID string) (int64, error) {
	return r.countRows(ctx, "conversation_summaries cs", conversationSummariesWhere(guildID, userID))
}

func (r *Repository) ListConversationSummaries(ctx context.Context, guildID, userID string, limit, offset int) ([]SummaryView, bool, error) {
	where := conversationSummariesWhere(guildID, userID)

	query := `
		SELECT cs.id,
		       cs.guild_id,
		       cs.user_id,
		       cs.summary_text,
		       cs.message_count,
		       cs.start_message_id,
		       cs.end_message_id,
		       cs.created_at,
		       cs.updated_at,
		       COALESCE(
		           (SELECT mb.author_nick
		            FROM message_buffer mb
		            WHERE mb.guild_id = cs.guild_id
		              AND mb.user_id = cs.user_id
		              AND mb.author_nick != ''
		            ORDER BY mb.timestamp DESC
		            LIMIT 1),
		           (SELECT dm.author_username
		            FROM discord_messages dm
		            WHERE dm.guild_id = cs.guild_id
		              AND dm.author_id = cs.user_id
		              AND dm.author_username != ''
		            ORDER BY dm.created_at DESC
		            LIMIT 1),
		           ''
		       ) AS author_name
		FROM conversation_summaries cs` + where.clause() + `
		ORDER BY cs.updated_at DESC, cs.id DESC
		LIMIT ? OFFSET ?`

	rows, err := r.conn.QueryContext(ctx, query, where.queryArgs(limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []SummaryView
	for rows.Next() {
		var (
			row          SummaryView
			startMessage sql.NullString
			endMessage   sql.NullString
		)
		if err := rows.Scan(
			&row.ID,
			&row.GuildID,
			&row.UserID,
			&row.SummaryText,
			&row.MessageCount,
			&startMessage,
			&endMessage,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.AuthorName,
		); err != nil {
			return nil, false, err
		}
		row.StartMessageID = startMessage.String
		row.EndMessageID = endMessage.String
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
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
func messageBufferWhere(guildID, channelID, userID string, processed int) whereBuilder {
	var where whereBuilder
	if guildID != "" {
		where.add("guild_id = ?", guildID)
	}
	if channelID != "" {
		where.add("channel_id = ?", channelID)
	}
	if userID != "" {
		where.add("user_id = ?", userID)
	}
	switch processed {
	case 0:
		// processed is nullable (DEFAULT 0); a NULL row is unprocessed, as the
		// list renders it.
		where.add("(processed = 0 OR processed IS NULL)")
	case 1:
		where.add("processed = 1")
	}
	return where
}

// CountMessageBuffer counts buffered messages under the same filters.
func (r *Repository) CountMessageBuffer(ctx context.Context, guildID, channelID, userID string, processed int) (int64, error) {
	return r.countRows(ctx, "message_buffer", messageBufferWhere(guildID, channelID, userID, processed))
}

func (r *Repository) ListMessageBuffer(ctx context.Context, guildID, channelID, userID string, processed int, limit, offset int) ([]BufferMessageView, bool, error) {
	where := messageBufferWhere(guildID, channelID, userID, processed)

	query := `
		SELECT id, guild_id, channel_id, message_id, user_id, author_nick, content,
		       has_image, image_description, timestamp, processed
		FROM message_buffer` + where.clause() + `
		ORDER BY timestamp DESC, id DESC
		LIMIT ? OFFSET ?`

	rows, err := r.conn.QueryContext(ctx, query, where.queryArgs(limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []BufferMessageView
	for rows.Next() {
		var (
			row              BufferMessageView
			hasImage         sql.NullBool
			imageDescription sql.NullString
			processedFlag    sql.NullBool
		)
		if err := rows.Scan(
			&row.ID,
			&row.GuildID,
			&row.ChannelID,
			&row.MessageID,
			&row.UserID,
			&row.AuthorNick,
			&row.Content,
			&hasImage,
			&imageDescription,
			&row.Timestamp,
			&processedFlag,
		); err != nil {
			return nil, false, err
		}
		row.HasImage = hasImage.Bool
		row.ImageDescription = imageDescription.String
		row.Processed = processedFlag.Bool
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// ListDiscordMessages returns cached Discord messages, newest first.
func discordMessagesWhere(guildID, channelID, authorID, text string, includeDeleted bool) whereBuilder {
	var where whereBuilder
	if guildID != "" {
		where.add("guild_id = ?", guildID)
	}
	if channelID != "" {
		where.add("channel_id = ?", channelID)
	}
	if authorID != "" {
		where.add("author_id = ?", authorID)
	}
	if text != "" {
		where.add("INSTR(LOWER(content), LOWER(?)) > 0", text)
	}
	if !includeDeleted {
		where.add("deleted_at IS NULL")
	}
	return where
}

// CountDiscordMessages counts cached messages under the same filters.
func (r *Repository) CountDiscordMessages(ctx context.Context, guildID, channelID, authorID, text string, includeDeleted bool) (int64, error) {
	return r.countRows(ctx, "discord_messages", discordMessagesWhere(guildID, channelID, authorID, text, includeDeleted))
}

func (r *Repository) ListDiscordMessages(ctx context.Context, guildID, channelID, authorID, text string, includeDeleted bool, limit, offset int) ([]DiscordMessageView, bool, error) {
	where := discordMessagesWhere(guildID, channelID, authorID, text, includeDeleted)

	query := `
		SELECT message_id, channel_id, guild_id, author_id, author_username, content,
		       referenced_message_id, message_json, created_at, edited_at, deleted_at
		FROM discord_messages` + where.clause() + `
		ORDER BY created_at DESC, message_id DESC
		LIMIT ? OFFSET ?`

	rows, err := r.conn.QueryContext(ctx, query, where.queryArgs(limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []DiscordMessageView
	for rows.Next() {
		var (
			row       DiscordMessageView
			editedAt  sql.NullInt64
			deletedAt sql.NullInt64
		)
		if err := rows.Scan(
			&row.MessageID,
			&row.ChannelID,
			&row.GuildID,
			&row.AuthorID,
			&row.AuthorUsername,
			&row.Content,
			&row.ReferencedMessageID,
			&row.MessageJSON,
			&row.CreatedAt,
			&editedAt,
			&deletedAt,
		); err != nil {
			return nil, false, err
		}
		row.EditedAt = editedAt.Int64
		row.DeletedAt = deletedAt.Int64
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// ListAttachmentCache returns cached attachment results, newest first.
func attachmentCacheWhere(kind, provider, model string) whereBuilder {
	var where whereBuilder
	if kind != "" {
		where.add("cache_kind = ?", kind)
	}
	if provider != "" {
		where.add("provider = ?", provider)
	}
	if model != "" {
		where.add("model = ?", model)
	}
	return where
}

// CountAttachmentCache counts cached attachments under the same filters.
func (r *Repository) CountAttachmentCache(ctx context.Context, kind, provider, model string) (int64, error) {
	return r.countRows(ctx, "attachment_cache", attachmentCacheWhere(kind, provider, model))
}

func (r *Repository) ListAttachmentCache(ctx context.Context, kind, provider, model string, limit, offset int) ([]AttachmentCacheView, bool, error) {
	where := attachmentCacheWhere(kind, provider, model)

	query := `
		SELECT id, cache_kind, attachment_key, source_url, filename, content_type, size_bytes,
		       provider, model, instruction_version, content, created_at, updated_at
		FROM attachment_cache` + where.clause() + `
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`

	rows, err := r.conn.QueryContext(ctx, query, where.queryArgs(limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []AttachmentCacheView
	for rows.Next() {
		var row AttachmentCacheView
		if err := rows.Scan(
			&row.ID,
			&row.CacheKind,
			&row.AttachmentKey,
			&row.SourceURL,
			&row.Filename,
			&row.ContentType,
			&row.SizeBytes,
			&row.Provider,
			&row.Model,
			&row.InstructionVersion,
			&row.Content,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, false, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// DeleteAttachmentCacheByID removes one attachment cache row.
func (r *Repository) DeleteAttachmentCacheByID(ctx context.Context, id int64) (int64, error) {
	return r.queries.DeleteAttachmentCacheByID(ctx, id)
}

// ListMessageVotes returns votes ordered by sort: newest, good, or bad.
func messageVotesWhere(guildID string, botOnly bool) whereBuilder {
	var where whereBuilder
	if guildID != "" {
		where.add("guild_id = ?", guildID)
	}
	if botOnly {
		where.add("is_bot_reply = 1")
	}
	return where
}

// CountMessageVotes counts votes under the same filters (sort is irrelevant).
func (r *Repository) CountMessageVotes(ctx context.Context, guildID string, botOnly bool) (int64, error) {
	return r.countRows(ctx, "message_votes", messageVotesWhere(guildID, botOnly))
}

func (r *Repository) ListMessageVotes(ctx context.Context, guildID string, botOnly bool, sort string, limit, offset int) ([]MessageVote, bool, error) {
	where := messageVotesWhere(guildID, botOnly)

	orderBy := "created_at DESC, message_id"
	switch sort {
	case "good":
		orderBy = "good_count DESC, bad_count DESC, created_at DESC, message_id"
	case "bad":
		orderBy = "bad_count DESC, good_count DESC, created_at DESC, message_id"
	}

	query := `
		SELECT message_id, guild_id, channel_id, author_id, author_name, is_bot_reply,
		       content, reply_to_message_id, reply_to_content, provider, model, good_count,
		       bad_count, message_created_at, created_at, updated_at
		FROM message_votes` + where.clause() + `
		ORDER BY ` + orderBy + `
		LIMIT ? OFFSET ?`

	rows, err := r.conn.QueryContext(ctx, query, where.queryArgs(limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []MessageVote
	for rows.Next() {
		var row MessageVote
		if err := rows.Scan(
			&row.MessageID,
			&row.GuildID,
			&row.ChannelID,
			&row.AuthorID,
			&row.AuthorName,
			&row.IsBotReply,
			&row.Content,
			&row.ReplyToMessageID,
			&row.ReplyToContent,
			&row.Provider,
			&row.Model,
			&row.GoodCount,
			&row.BadCount,
			&row.MessageCreatedAt,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, false, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// ListBotEvents returns events inside the filter's time range, newest first.
// The timestamp range keeps the query on idx_bot_events_timestamp, optional
// predicates are only emitted when set, and LIMIT/OFFSET is always applied.
func botEventsWhere(filter EventListFilter) whereBuilder {
	where := whereBuilder{}
	where.add("timestamp >= ?", filter.From)
	where.add("timestamp <= ?", filter.To)
	if filter.EventType != "" {
		where.add("event_type = ?", filter.EventType)
	}
	if filter.GuildID != "" {
		where.add("guild_id = ?", filter.GuildID)
	}
	if filter.HasError {
		where.add("error IS NOT NULL AND error != ''")
	}
	return where
}

// CountBotEvents counts events inside the filter's time range and predicates.
func (r *Repository) CountBotEvents(ctx context.Context, filter EventListFilter) (int64, error) {
	return r.countRows(ctx, "bot_events", botEventsWhere(filter))
}

func (r *Repository) ListBotEvents(ctx context.Context, filter EventListFilter, limit, offset int) ([]BotEvent, bool, error) {
	where := botEventsWhere(filter)

	query := `
		SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
		FROM bot_events` + where.clause() + `
		ORDER BY timestamp DESC, id DESC
		LIMIT ? OFFSET ?`

	rows, err := r.conn.QueryContext(ctx, query, where.queryArgs(limit+1, offset)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []BotEvent
	for rows.Next() {
		var (
			row        BotEvent
			timestamp  int64
			eventType  string
			guildID    sql.NullString
			channelID  sql.NullString
			messageID  sql.NullString
			userID     sql.NullString
			details    sql.NullString
			durationMS sql.NullInt64
			errorText  sql.NullString
		)
		if err := rows.Scan(
			&timestamp,
			&eventType,
			&guildID,
			&channelID,
			&messageID,
			&userID,
			&details,
			&durationMS,
			&errorText,
		); err != nil {
			return nil, false, err
		}

		row.Timestamp = time.Unix(timestamp, 0)
		row.EventType = EventType(eventType)
		row.GuildID = guildID.String
		row.ChannelID = channelID.String
		row.MessageID = messageID.String
		row.UserID = userID.String
		row.DurationMS = durationMS.Int64
		row.Error = errorText.String
		if details.Valid && details.String != "" {
			var eventDetails EventDetails
			if err := json.Unmarshal([]byte(details.String), &eventDetails); err == nil {
				row.Details = &eventDetails
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// ListBotEventTypes returns the distinct event types present in bot_events.
func (r *Repository) ListBotEventTypes(ctx context.Context) ([]string, error) {
	return r.queries.ListBotEventTypes(ctx)
}
