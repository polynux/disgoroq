package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/db"
	"polynux/disgoroq/logger"
)

// repository implements the Repository interface using SQLC-generated code
type repository struct {
	queries *db.Queries
}

// NewRepository creates a new memory repository
func NewRepository(dbConn *sql.DB) Repository {
	return &repository{
		queries: db.New(dbConn),
	}
}

// Message buffer operations

func (r *repository) InsertMessageBuffer(ctx context.Context, message *BufferedMessage) error {
	params := db.InsertMessageBufferParams{
		GuildID:          message.GuildID,
		ChannelID:        message.ChannelID,
		MessageID:        message.MessageID,
		UserID:           message.UserID,
		AuthorNick:       message.AuthorNick,
		Content:          message.Content,
		HasImage:         sqlBool(message.HasImage),
		ImageDescription: sqlString(message.ImageDescription),
		Timestamp:        message.Timestamp.Unix(),
		Processed:        sqlBool(message.Processed),
	}
	return r.queries.InsertMessageBuffer(ctx, params)
}

func (r *repository) GetUnprocessedMessageCount(ctx context.Context, guildID, userID string) (int64, error) {
	count, err := r.queries.GetUnprocessedMessageCount(ctx, db.GetUnprocessedMessageCountParams{
		GuildID: guildID,
		UserID:  userID,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get unprocessed message count: %w", err)
	}
	return count, nil
}

func (r *repository) GetUnprocessedMessages(ctx context.Context, guildID, userID string, limit int32) ([]BufferedMessage, error) {
	rows, err := r.queries.GetUnprocessedMessages(ctx, db.GetUnprocessedMessagesParams{
		GuildID: guildID,
		UserID:  userID,
		Limit:   int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get unprocessed messages: %w", err)
	}

	messages := make([]BufferedMessage, len(rows))
	for i, row := range rows {
		messages[i] = BufferedMessage{
			ID:               row.ID,
			GuildID:          row.GuildID,
			ChannelID:        row.ChannelID,
			MessageID:        row.MessageID,
			UserID:           row.UserID,
			AuthorNick:       row.AuthorNick,
			Content:          row.Content,
			HasImage:         row.HasImage.Bool,
			ImageDescription: row.ImageDescription.String,
			Timestamp:        time.Unix(row.Timestamp, 0),
			Processed:        row.Processed.Bool,
		}
	}
	return messages, nil
}

func (r *repository) MarkMessagesAsProcessed(ctx context.Context, guildID, userID string, limit int32) error {
	return r.queries.MarkMessagesAsProcessed(ctx, db.MarkMessagesAsProcessedParams{
		GuildID: guildID,
		UserID:  userID,
		Limit:   int64(limit),
	})
}

func (r *repository) DeleteProcessedMessages(ctx context.Context, timestamp time.Time) error {
	return r.queries.DeleteProcessedMessages(ctx, timestamp.Unix())
}

// Summary operations

func (r *repository) InsertSummary(ctx context.Context, summary *Summary) error {
	var embeddingBlob []byte
	var err error

	// Only convert embedding if it's not nil and has the correct dimensions
	if summary.Embedding != nil && len(summary.Embedding) > 0 {
		embeddingBlob, err = float32SliceToF32Blob(summary.Embedding)
		if err != nil {
			if logger.IsDebugMode() {
				logger.Debug("Failed to convert memory embedding to blob",
					zap.Error(err),
					zap.String("user_id", summary.UserID),
					zap.String("guild_id", summary.GuildID))
			}
			embeddingBlob = nil // Store as NULL instead of invalid data
		}
	}

	params := db.InsertSummaryParams{
		GuildID:        summary.GuildID,
		UserID:         summary.UserID,
		SummaryText:    summary.SummaryText,
		MessageCount:   summary.MessageCount,
		StartMessageID: sqlString(summary.StartMessageID),
		EndMessageID:   sqlString(summary.EndMessageID),
		CreatedAt:      summary.CreatedAt.Unix(),
		UpdatedAt:      summary.UpdatedAt.Unix(),
		Embedding:      embeddingBlob,
	}

	err = r.queries.InsertSummary(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to insert summary: %w", err)
	}
	return nil
}

func (r *repository) GetLatestSummaryForUser(ctx context.Context, guildID, userID string) (*Summary, error) {
	row, err := r.queries.GetLatestSummaryForUser(ctx, db.GetLatestSummaryForUserParams{
		GuildID: guildID,
		UserID:  userID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get latest summary for user: %w", err)
	}

	embedding, err := f32BlobToFloat32Slice(row.Embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert embedding from blob: %w", err)
	}

	return &Summary{
		ID:             row.ID,
		GuildID:        row.GuildID,
		UserID:         row.UserID,
		SummaryText:    row.SummaryText,
		MessageCount:   row.MessageCount,
		StartMessageID: row.StartMessageID.String,
		EndMessageID:   row.EndMessageID.String,
		CreatedAt:      time.Unix(row.CreatedAt, 0),
		UpdatedAt:      time.Unix(row.UpdatedAt, 0),
		Embedding:      embedding,
	}, nil
}

func (r *repository) GetLatestSummariesForUsers(ctx context.Context, guildID string, userIDs []string) ([]Summary, error) {
	// SQLC doesn't handle slice parameters well, so we'll get all summaries and filter in memory
	// This is less efficient but works for now. In production, you'd want to fix the SQL query.
	allSummaries, err := r.GetSummariesByGuild(ctx, guildID, 1000, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest summaries for users: %w", err)
	}

	// Filter to only requested users and get latest for each
	userSummaryMap := make(map[string]Summary)
	userIDSet := make(map[string]bool)
	for _, userID := range userIDs {
		userIDSet[userID] = true
	}

	for _, summary := range allSummaries {
		if userIDSet[summary.UserID] {
			// Keep the most recent summary for each user
			if existing, exists := userSummaryMap[summary.UserID]; !exists || summary.UpdatedAt.After(existing.UpdatedAt) {
				userSummaryMap[summary.UserID] = summary
			}
		}
	}

	// Convert map to slice
	result := make([]Summary, 0, len(userSummaryMap))
	for _, summary := range userSummaryMap {
		result = append(result, summary)
	}

	return result, nil
}

func (r *repository) GetSummariesByGuild(ctx context.Context, guildID string, limit, offset int32) ([]Summary, error) {
	rows, err := r.queries.GetSummariesByGuild(ctx, db.GetSummariesByGuildParams{
		GuildID: guildID,
		Limit:   int64(limit),
		Offset:  int64(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get summaries by guild: %w", err)
	}

	summaries := make([]Summary, len(rows))
	for i, row := range rows {
		embedding, err := f32BlobToFloat32Slice(row.Embedding)
		if err != nil {
			return nil, fmt.Errorf("failed to convert embedding from blob: %w", err)
		}

		summaries[i] = Summary{
			ID:             row.ID,
			GuildID:        row.GuildID,
			UserID:         row.UserID,
			SummaryText:    row.SummaryText,
			MessageCount:   row.MessageCount,
			StartMessageID: row.StartMessageID.String,
			EndMessageID:   row.EndMessageID.String,
			CreatedAt:      time.Unix(row.CreatedAt, 0),
			UpdatedAt:      time.Unix(row.UpdatedAt, 0),
			Embedding:      embedding,
		}
	}
	return summaries, nil
}

func (r *repository) UpdateSummary(ctx context.Context, summary *Summary) error {
	embeddingBlob, err := float32SliceToF32Blob(summary.Embedding)
	if err != nil {
		return fmt.Errorf("failed to convert embedding to blob: %w", err)
	}

	params := db.UpdateSummaryParams{
		SummaryText:  summary.SummaryText,
		MessageCount: summary.MessageCount,
		EndMessageID: sqlString(summary.EndMessageID),
		UpdatedAt:    summary.UpdatedAt.Unix(),
		Embedding:    embeddingBlob,
		ID:           summary.ID,
	}

	return r.queries.UpdateSummary(ctx, params)
}

func (r *repository) DeleteSummariesForUser(ctx context.Context, guildID, userID string) error {
	return r.queries.DeleteSummariesForUser(ctx, db.DeleteSummariesForUserParams{
		GuildID: guildID,
		UserID:  userID,
	})
}

func (r *repository) ClearUserMemory(ctx context.Context, userID, guildID string) error {
	return r.queries.DeleteSummariesForUser(ctx, db.DeleteSummariesForUserParams{
		GuildID: guildID,
		UserID:  userID,
	})
}

func (r *repository) CreateMessageBufferEntry(ctx context.Context, entry *MessageBufferEntry) error {
	// Generate a unique message ID if not provided (to avoid UNIQUE constraint violations)
	messageID := entry.MessageID
	if messageID == "" {
		messageID = fmt.Sprintf("buf_%d_%s", time.Now().UnixNano(), entry.UserID)
	}

	message := &BufferedMessage{
		GuildID:    entry.GuildID,
		ChannelID:  entry.ChannelID,
		UserID:     entry.UserID,
		AuthorNick: entry.AuthorNick,
		Content:    entry.Content,
		Timestamp:  entry.CreatedAt,
		Processed:  false,
		MessageID:  messageID,
	}

	err := r.InsertMessageBuffer(ctx, message)
	if err != nil {
		// Check if it's a duplicate error - if so, just ignore it
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			if logger.IsDebugMode() {
				logger.Debug("Skipping duplicate memory message",
					zap.String("message_id", messageID),
					zap.String("user_id", entry.UserID),
					zap.String("guild_id", entry.GuildID))
			}
			return nil // Message already exists, not an error
		}
		return err
	}
	if logger.IsDebugMode() {
		logger.Debug("Inserted memory message",
			zap.String("message_id", messageID),
			zap.String("user_id", entry.UserID),
			zap.String("guild_id", entry.GuildID))
	}
	return nil
}

func (r *repository) GetMessageBufferByUserGuild(ctx context.Context, userID, guildID string, limit int) ([]*MessageBufferEntry, error) {
	messages, err := r.GetUnprocessedMessages(ctx, guildID, userID, int32(limit))
	if err != nil {
		return nil, err
	}

	entries := make([]*MessageBufferEntry, len(messages))
	for i, msg := range messages {
		entries[i] = &MessageBufferEntry{
			ID:         msg.ID,
			UserID:     msg.UserID,
			GuildID:    msg.GuildID,
			ChannelID:  msg.ChannelID,
			AuthorNick: msg.AuthorNick,
			Content:    msg.Content,
			CreatedAt:  msg.Timestamp,
			MessageID:  msg.MessageID,
		}
	}
	return entries, nil
}

func (r *repository) DeleteMessageBufferEntries(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	for _, id := range ids {
		if err := r.queries.DeleteMessageBufferEntry(ctx, id); err != nil {
			return fmt.Errorf("failed to delete message buffer entry %d: %w", id, err)
		}
	}

	return nil
}

func (r *repository) GetRecentMessages(ctx context.Context, userID, guildID string, limit int) ([]*MessageBufferEntry, error) {
	return r.GetMessageBufferByUserGuild(ctx, userID, guildID, limit)
}

func (r *repository) CreateSummary(ctx context.Context, summary *ConversationSummary) error {
	s := &Summary{
		GuildID:      summary.GuildID,
		UserID:       summary.UserID,
		SummaryText:  summary.Content,
		CreatedAt:    summary.CreatedAt,
		UpdatedAt:    summary.CreatedAt,
		Embedding:    summary.Embedding,
		MessageCount: 1,
	}
	return r.InsertSummary(ctx, s)
}

func (r *repository) GetLatestSummary(ctx context.Context, userID, guildID string) (*ConversationSummary, error) {
	summary, err := r.GetLatestSummaryForUser(ctx, guildID, userID)
	if err != nil || summary == nil {
		return nil, err
	}

	return &ConversationSummary{
		ID:        summary.ID,
		UserID:    summary.UserID,
		GuildID:   summary.GuildID,
		Content:   summary.SummaryText,
		Embedding: summary.Embedding,
		CreatedAt: summary.CreatedAt,
	}, nil
}

func (r *repository) GetSummariesByUserGuild(ctx context.Context, userID, guildID string) ([]*ConversationSummary, error) {
	summaries, err := r.GetSummariesByGuild(ctx, guildID, 1000, 0)
	if err != nil {
		return nil, err
	}

	var result []*ConversationSummary
	for _, s := range summaries {
		if s.UserID == userID {
			result = append(result, &ConversationSummary{
				ID:        s.ID,
				UserID:    s.UserID,
				GuildID:   s.GuildID,
				Content:   s.SummaryText,
				Embedding: s.Embedding,
				CreatedAt: s.CreatedAt,
			})
		}
	}
	return result, nil
}

func (r *repository) FindRelevantSummaries(ctx context.Context, userID, guildID string, queryEmbedding []float32, limit int) ([]*RelevantSummary, error) {
	// Check if there are any summaries with valid embeddings first
	allSummaries, err := r.GetSummariesByUserGuild(ctx, userID, guildID)
	if err != nil {
		return nil, err
	}

	hasValidEmbeddings := false
	for _, s := range allSummaries {
		if s.Embedding != nil && len(s.Embedding) == 768 {
			hasValidEmbeddings = true
			break
		}
	}

	if !hasValidEmbeddings {
		if logger.IsDebugMode() {
			logger.Debug("No valid memory embeddings found for vector search",
				zap.String("user_id", userID),
				zap.String("guild_id", guildID))
		}
		return []*RelevantSummary{}, nil
	}

	results, err := r.VectorSearchSummariesByUser(ctx, guildID, userID, queryEmbedding, int32(limit))
	if err != nil {
		// If vector search fails (e.g., invalid embeddings in DB), log and return empty
		if logger.IsDebugMode() {
			logger.Debug("Memory vector search failed due to invalid embeddings",
				zap.String("user_id", userID),
				zap.String("guild_id", guildID),
				zap.Error(err))
		}
		return []*RelevantSummary{}, nil
	}

	summaries := make([]*RelevantSummary, len(results))
	for i, result := range results {
		summaries[i] = &RelevantSummary{
			ConversationSummary: &ConversationSummary{
				ID:        result.Summary.ID,
				UserID:    result.Summary.UserID,
				GuildID:   result.Summary.GuildID,
				Content:   result.Summary.SummaryText,
				Embedding: result.Summary.Embedding,
				CreatedAt: result.Summary.CreatedAt,
			},
			Similarity: float64(2.0 - result.Distance),
		}
	}
	return summaries, nil
}

func (r *repository) GetGuildMemorySettings(ctx context.Context, guildID string) (*GuildMemorySettings, error) {
	return &GuildMemorySettings{
		GuildID:         guildID,
		Enabled:         true,
		BufferThreshold: 10,
		SummaryInterval: time.Hour,
	}, nil
}

func (r *repository) UpdateGuildMemorySettings(ctx context.Context, settings *GuildMemorySettings) error {
	return nil
}

// Vector search operations

func (r *repository) VectorSearchSummaries(ctx context.Context, guildID string, embedding []float32, limit int32) ([]VectorSearchResult, error) {
	embeddingBlob, err := float32SliceToF32Blob(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert embedding to blob: %w", err)
	}

	rows, err := r.queries.VectorSearchSummaries(ctx, db.VectorSearchSummariesParams{
		Vector32: embeddingBlob,
		GuildID:  guildID,
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to perform vector search: %w", err)
	}

	results := make([]VectorSearchResult, len(rows))
	for i, row := range rows {
		summary := Summary{
			ID:             row.ID,
			GuildID:        row.GuildID,
			UserID:         row.UserID,
			SummaryText:    row.SummaryText,
			MessageCount:   row.MessageCount,
			StartMessageID: row.StartMessageID.String,
			EndMessageID:   row.EndMessageID.String,
			CreatedAt:      time.Unix(row.CreatedAt, 0),
			UpdatedAt:      time.Unix(row.UpdatedAt, 0),
			Embedding:      nil, // Fetch separately if needed - not returned by vector search
		}

		// Convert distance from interface{} to float32
		var distance float32
		switch d := row.Distance.(type) {
		case float64:
			distance = float32(d)
		case float32:
			distance = d
		case int64:
			distance = float32(d)
		default:
			return nil, fmt.Errorf("unexpected distance type: %T", row.Distance)
		}

		results[i] = VectorSearchResult{
			Summary:  summary,
			Distance: distance,
		}
	}
	return results, nil
}

func (r *repository) VectorSearchSummariesByUser(ctx context.Context, guildID, userID string, embedding []float32, limit int32) ([]VectorSearchResult, error) {
	embeddingBlob, err := float32SliceToF32Blob(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert embedding to blob: %w", err)
	}

	rows, err := r.queries.VectorSearchSummariesByUser(ctx, db.VectorSearchSummariesByUserParams{
		Vector32: embeddingBlob,
		GuildID:  guildID,
		UserID:   userID,
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to perform vector search by user: %w", err)
	}

	results := make([]VectorSearchResult, len(rows))
	for i, row := range rows {
		summary := Summary{
			ID:             row.ID,
			GuildID:        row.GuildID,
			UserID:         row.UserID,
			SummaryText:    row.SummaryText,
			MessageCount:   row.MessageCount,
			StartMessageID: row.StartMessageID.String,
			EndMessageID:   row.EndMessageID.String,
			CreatedAt:      time.Unix(row.CreatedAt, 0),
			UpdatedAt:      time.Unix(row.UpdatedAt, 0),
			Embedding:      nil, // Fetch separately if needed
		}

		// Convert distance from interface{} to float32
		var distance float32
		switch d := row.Distance.(type) {
		case float64:
			distance = float32(d)
		case float32:
			distance = d
		case int64:
			distance = float32(d)
		default:
			return nil, fmt.Errorf("unexpected distance type: %T", row.Distance)
		}

		results[i] = VectorSearchResult{
			Summary:  summary,
			Distance: distance,
		}
	}
	return results, nil
}

func (r *repository) GetSummaryStatsByGuild(ctx context.Context, guildID string) (*MemoryStats, error) {
	stats, err := r.queries.GetSummaryStatsByGuild(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get summary stats: %w", err)
	}

	// Convert sql.NullFloat64 to int64
	var totalMessages int64
	if stats.TotalMessagesSummarized.Valid {
		totalMessages = int64(stats.TotalMessagesSummarized.Float64)
	}

	// Convert interface{} to int64 for timestamp
	var lastUpdate int64
	switch v := stats.LastSummaryUpdate.(type) {
	case int64:
		lastUpdate = v
	case float64:
		lastUpdate = int64(v)
	case int:
		lastUpdate = int64(v)
	case nil:
		lastUpdate = 0
	default:
		return nil, fmt.Errorf("unexpected last_summary_update type: %T", stats.LastSummaryUpdate)
	}

	return &MemoryStats{
		TotalSummaries:          stats.TotalSummaries,
		UniqueUsers:             stats.UniqueUsers,
		TotalMessagesSummarized: totalMessages,
		LastSummaryUpdate:       time.Unix(lastUpdate, 0),
	}, nil
}

// Helper functions

func sqlBool(b bool) sql.NullBool {
	return sql.NullBool{Bool: b, Valid: true}
}

func sqlString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
