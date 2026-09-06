package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/emoji"
	"polynux/disgoroq/logger"
)

// MemoryService implements the main memory management logic
type MemoryService struct {
	repo       Repository
	embeddings EmbeddingProvider
	summarizer *Summarizer

	// Configuration
	bufferThreshold    int           // Number of messages before summarization
	summaryInterval    time.Duration // Minimum time between summaries
	maxContextMessages int           // Max recent messages to include in context

	// Concurrency control
	activeSummaries sync.Map // map[string]*sync.Once (userID+guildID -> summarization control)
	summaryMu       sync.RWMutex

	// Context building
	maxSummaryContext int // Max number of summaries to include in context
}

// ServiceConfig holds configuration for the memory service
type ServiceConfig struct {
	BufferThreshold    int
	SummaryInterval    time.Duration
	MaxContextMessages int
	MaxSummaryContext  int
}

// DefaultServiceConfig returns sensible defaults
func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		BufferThreshold:    10,            // Summarize after 10 messages
		SummaryInterval:    1 * time.Hour, // Minimum 1 hour between summaries
		MaxContextMessages: 5,             // Include last 5 messages in context
		MaxSummaryContext:  3,             // Include top 3 relevant summaries
	}
}

// Ensure MemoryService implements the Service interface
var _ Service = (*MemoryService)(nil)

// NewService creates a new memory service
func NewService(repo Repository, embeddings EmbeddingProvider, summarizer *Summarizer, config ServiceConfig) Service {
	if config.BufferThreshold <= 0 {
		config.BufferThreshold = DefaultServiceConfig().BufferThreshold
	}
	if config.SummaryInterval <= 0 {
		config.SummaryInterval = DefaultServiceConfig().SummaryInterval
	}
	if config.MaxContextMessages <= 0 {
		config.MaxContextMessages = DefaultServiceConfig().MaxContextMessages
	}
	if config.MaxSummaryContext <= 0 {
		config.MaxSummaryContext = DefaultServiceConfig().MaxSummaryContext
	}

	return &MemoryService{
		repo:               repo,
		embeddings:         embeddings,
		summarizer:         summarizer,
		bufferThreshold:    config.BufferThreshold,
		summaryInterval:    config.SummaryInterval,
		maxContextMessages: config.MaxContextMessages,
		maxSummaryContext:  config.MaxSummaryContext,
	}
}

// BufferMessage stores a message in the buffer and triggers summarization if needed
func (s *MemoryService) BufferMessage(ctx context.Context, input BufferMessageInput) error {
	content := emoji.NormalizeDiscordEmojiShortcodes(input.Content)

	if logger.IsDebugMode() {
		logger.Debug("Buffering memory message",
			zap.String("user_id", input.UserID),
			zap.String("guild_id", input.GuildID),
			zap.String("channel_id", input.ChannelID))
	}

	// Use the real Discord message ID; fall back to a synthetic one only when absent
	messageID := input.DiscordMessageID
	if messageID == "" {
		messageID = fmt.Sprintf("msg_%d_%s_%d", time.Now().UnixNano(), input.UserID, time.Now().Nanosecond())
	}

	timestamp := input.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	// Store the message in buffer
	message := &MessageBufferEntry{
		UserID:     input.UserID,
		GuildID:    input.GuildID,
		Content:    content,
		CreatedAt:  timestamp,
		MessageID:  messageID,
		AuthorNick: input.AuthorName,
		ChannelID:  input.ChannelID,
	}

	if err := s.repo.CreateMessageBufferEntry(ctx, message); err != nil {
		return fmt.Errorf("[Memory] failed to buffer message: %w", err)
	}

	if logger.IsDebugMode() {
		logger.Debug("Buffered memory message",
			zap.String("user_id", input.UserID),
			zap.String("guild_id", input.GuildID),
			zap.String("channel_id", input.ChannelID),
			zap.String("message_id", messageID))
	}

	// Check if we should trigger summarization
	go s.checkAndSummarizeAsync(input.UserID, input.GuildID)

	return nil
}

// checkAndSummarizeAsync runs summarization check in background
func (s *MemoryService) checkAndSummarizeAsync(userID, guildID string) {
	key := fmt.Sprintf("%s:%s", userID, guildID)

	// Check if summarization is already running for this user/guild
	if _, loaded := s.activeSummaries.LoadOrStore(key, true); loaded {
		if logger.IsDebugMode() {
			logger.Debug("Memory summarization already running",
				zap.String("user_id", userID),
				zap.String("guild_id", guildID))
		}
		return
	}

	// Clean up after summarization completes
	defer s.activeSummaries.Delete(key)

	// Small delay to let main transaction complete
	time.Sleep(100 * time.Millisecond)

	ctx := context.Background()
	if logger.IsDebugMode() {
		logger.Debug("Checking memory summarization eligibility",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID))
	}
	if err := s.runSummarization(ctx, userID, guildID); err != nil {
		logger.Warn("Memory summarization failed",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Error(err))
	}
}

// runSummarization performs the actual summarization
func (s *MemoryService) runSummarization(ctx context.Context, userID, guildID string) error {
	if logger.IsDebugMode() {
		logger.Debug("Running memory summarization check",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID))
	}

	// Check if enough time has passed since last summary
	lastSummary, err := s.repo.GetLatestSummary(ctx, userID, guildID)
	if err == nil && lastSummary != nil {
		timeSinceLast := time.Since(lastSummary.CreatedAt)
		if timeSinceLast < s.summaryInterval {
			if logger.IsDebugMode() {
				logger.Debug("Skipping memory summarization due to interval",
					zap.String("user_id", userID),
					zap.String("guild_id", guildID),
					zap.Duration("time_since_last", timeSinceLast),
					zap.Duration("required_interval", s.summaryInterval))
			}
			return nil // Too soon for another summary
		}
	}

	// Get buffered messages
	messages, err := s.repo.GetMessageBufferByUserGuild(ctx, userID, guildID, s.bufferThreshold)
	if err != nil {
		return fmt.Errorf("failed to get buffered messages: %w", err)
	}

	if logger.IsDebugMode() {
		logger.Debug("Loaded buffered memory messages",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Int("message_count", len(messages)),
			zap.Int("threshold", s.bufferThreshold))
	}

	if len(messages) < s.bufferThreshold {
		if logger.IsDebugMode() {
			logger.Debug("Not enough memory messages to summarize",
				zap.String("user_id", userID),
				zap.String("guild_id", guildID),
				zap.Int("message_count", len(messages)),
				zap.Int("threshold", s.bufferThreshold))
		}
		return nil // Not enough messages yet
	}

	logger.Info("Triggering memory summarization",
		zap.String("user_id", userID),
		zap.String("guild_id", guildID),
		zap.Int("message_count", len(messages)))

	// Generate conversation text from messages

	// Get existing summary for incremental update
	var existingSummary *ConversationSummary
	if lastSummary != nil {
		existingSummary = lastSummary
	}

	// Generate new summary
	summarizationRequest := &SummarizationRequest{
		GuildID:         guildID,
		UserID:          userID,
		Messages:        messagesToBufferedMessages(messages),
		PreviousSummary: conversationSummaryToSummary(existingSummary),
		NewMessageCount: len(messages),
	}

	result, err := s.summarizer.Summarize(ctx, summarizationRequest)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	if !result.Success || result.Summary == nil {
		return fmt.Errorf("summarization failed: %v", result.Error)
	}

	// Generate embedding for the summary
	summaryEmbedding, err := s.embeddings.GenerateEmbedding(ctx, result.Summary.SummaryText)
	if err != nil {
		logger.Warn("Failed to generate summary embedding",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Error(err))
		summaryEmbedding = nil
	}

	// Store the summary
	summary := &ConversationSummary{
		UserID:    userID,
		GuildID:   guildID,
		Content:   result.Summary.SummaryText,
		Embedding: summaryEmbedding,
		Quality:   0.8, // Default quality score
		CreatedAt: time.Now(),
	}

	if err := s.repo.CreateSummary(ctx, summary); err != nil {
		return fmt.Errorf("failed to store summary: %w", err)
	}

	// Clear the processed messages from buffer
	messageIDs := make([]int64, len(messages))
	for i, msg := range messages {
		messageIDs[i] = msg.ID
	}
	if err := s.repo.DeleteMessageBufferEntries(ctx, messageIDs); err != nil {
		logger.Warn("Failed to clear processed memory messages",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Error(err))
	}

	logger.Info("Created memory summary",
		zap.String("user_id", userID),
		zap.String("guild_id", guildID),
		zap.Float64("quality", 0.8))
	return nil
}

// Helper functions to convert between types
func messagesToBufferedMessages(messages []*MessageBufferEntry) []BufferedMessage {
	result := make([]BufferedMessage, len(messages))
	for i, msg := range messages {
		nick := msg.AuthorNick
		if nick == "" {
			nick = msg.UserID
		}
		result[i] = BufferedMessage{
			ID:         msg.ID,
			GuildID:    msg.GuildID,
			ChannelID:  msg.ChannelID,
			MessageID:  msg.MessageID,
			UserID:     msg.UserID,
			AuthorNick: nick,
			Content:    emoji.NormalizeDiscordEmojiShortcodes(msg.Content),
			Timestamp:  msg.CreatedAt,
		}
	}
	return result
}

func conversationSummaryToSummary(summary *ConversationSummary) *Summary {
	if summary == nil {
		return nil
	}
	return &Summary{
		ID:          summary.ID,
		GuildID:     summary.GuildID,
		UserID:      summary.UserID,
		SummaryText: summary.Content,
		CreatedAt:   summary.CreatedAt,
		UpdatedAt:   summary.CreatedAt,
		Embedding:   summary.Embedding,
	}
}

// messagesToConversation converts buffered messages to conversation text
func (s *MemoryService) messagesToConversation(messages []*MessageBufferEntry) string {
	var conversation string
	for _, msg := range messages {
		conversation += fmt.Sprintf("[%s] %s\n", msg.CreatedAt.Format("15:04"), emoji.NormalizeDiscordEmojiShortcodes(msg.Content))
	}
	return conversation
}

// GetMemoryContext builds context for AI responses
func (s *MemoryService) GetMemoryContext(ctx context.Context, userID, guildID, currentMessage string) (*MemoryContext, error) {
	currentMessage = emoji.NormalizeDiscordEmojiShortcodes(currentMessage)

	if logger.IsDebugMode() {
		logger.Debug("Building memory context",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.String("message_preview", truncateMemoryText(currentMessage, 30)))
	}

	context := &MemoryContext{
		Summaries:       []SummaryContext{},
		RecentMessages:  []string{},
		ConfidenceScore: 0.0,
	}

	// First, get ALL summaries for this user (fallback if vector search fails)
	allSummaries, err := s.repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if err != nil {
		if logger.IsDebugMode() {
			logger.Debug("Failed to load user memory summaries",
				zap.String("user_id", userID),
				zap.String("guild_id", guildID),
				zap.Error(err))
		}
	} else if logger.IsDebugMode() {
		logger.Debug("Loaded user memory summaries",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Int("summary_count", len(allSummaries)))
	}

	// Get relevant summaries using vector similarity
	if s.embeddings != nil {
		queryEmbedding, err := s.embeddings.GenerateEmbedding(ctx, currentMessage)
		if err != nil {
			if logger.IsDebugMode() {
				logger.Debug("Failed to generate memory query embedding",
					zap.String("user_id", userID),
					zap.String("guild_id", guildID),
					zap.Error(err))
			}
		} else if queryEmbedding != nil {
			if logger.IsDebugMode() {
				logger.Debug("Searching relevant memory summaries",
					zap.String("user_id", userID),
					zap.String("guild_id", guildID))
			}
			summaries, err := s.repo.FindRelevantSummaries(ctx, userID, guildID, queryEmbedding, s.maxSummaryContext)
			if err != nil {
				if logger.IsDebugMode() {
					logger.Debug("Memory vector search failed",
						zap.String("user_id", userID),
						zap.String("guild_id", guildID),
						zap.Error(err))
				}
			} else if logger.IsDebugMode() {
				logger.Debug("Memory vector search completed",
					zap.String("user_id", userID),
					zap.String("guild_id", guildID),
					zap.Int("summary_count", len(summaries)))
				for _, summary := range summaries {
					context.Summaries = append(context.Summaries, SummaryContext{
						Content:   summary.Content,
						CreatedAt: summary.CreatedAt,
						Relevance: summary.Similarity,
					})
				}
			}
		}
	} else {
		logger.Debug("Memory embedding provider unavailable")
	}

	// Fallback: if vector search returned nothing but we have summaries, use the most recent one
	if len(context.Summaries) == 0 && len(allSummaries) > 0 {
		logger.Debug("Using fallback memory summary",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID))
		// Find most recent summary
		var mostRecent *ConversationSummary
		for _, s := range allSummaries {
			if mostRecent == nil || s.CreatedAt.After(mostRecent.CreatedAt) {
				mostRecent = s
			}
		}
		if mostRecent != nil {
			context.Summaries = append(context.Summaries, SummaryContext{
				Content:   mostRecent.Content,
				CreatedAt: mostRecent.CreatedAt,
				Relevance: 0.5, // Default relevance for fallback
			})
		}
	}

	// Get recent messages
	recentMessages, err := s.repo.GetRecentMessages(ctx, userID, guildID, s.maxContextMessages)
	if err == nil {
		for _, msg := range recentMessages {
			context.RecentMessages = append(context.RecentMessages, emoji.NormalizeDiscordEmojiShortcodes(msg.Content))
		}
	}

	// Calculate confidence score based on summary relevance
	if len(context.Summaries) > 0 {
		totalRelevance := 0.0
		for i, summary := range context.Summaries {
			context.Summaries[i].Content = emoji.NormalizeDiscordEmojiShortcodes(summary.Content)
			totalRelevance += summary.Relevance
		}
		context.ConfidenceScore = totalRelevance / float64(len(context.Summaries))
	}

	return context, nil
}

// GetGuildSettings retrieves memory settings for a guild
func (s *MemoryService) GetGuildSettings(ctx context.Context, guildID string) (*GuildMemorySettings, error) {
	settings, err := s.repo.GetGuildMemorySettings(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get guild settings: %w", err)
	}

	if settings == nil {
		// Return default settings
		settings = &GuildMemorySettings{
			GuildID:         guildID,
			Enabled:         true,
			BufferThreshold: s.bufferThreshold,
			SummaryInterval: s.summaryInterval,
		}
	}

	return settings, nil
}

// UpdateGuildSettings updates memory settings for a guild
func (s *MemoryService) UpdateGuildSettings(ctx context.Context, settings *GuildMemorySettings) error {
	return s.repo.UpdateGuildMemorySettings(ctx, settings)
}

// ClearUserMemory clears all memory for a specific user in a guild
func (s *MemoryService) ClearUserMemory(ctx context.Context, userID, guildID string) error {
	return s.repo.ClearUserMemory(ctx, userID, guildID)
}

// GetUserSummaries retrieves all summaries for a user in a guild
func (s *MemoryService) GetUserSummaries(ctx context.Context, userID, guildID string) ([]*ConversationSummary, error) {
	return s.repo.GetSummariesByUserGuild(ctx, userID, guildID)
}

// GetLatestSummary retrieves the most recent summary for a user in a guild
func (s *MemoryService) GetLatestSummary(ctx context.Context, userID, guildID string) (*ConversationSummary, error) {
	return s.repo.GetLatestSummary(ctx, userID, guildID)
}

// VectorSearch performs vector similarity search on conversation summaries
func (s *MemoryService) VectorSearch(ctx context.Context, request *VectorSearchRequest) ([]VectorSearchResult, error) {
	if s.embeddings == nil {
		return nil, fmt.Errorf("embedding provider not available")
	}

	// Generate embedding for the search query
	_, err := s.embeddings.GenerateEmbedding(ctx, request.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// For now, return empty results since we don't have vector search in the repository interface
	// This would need to be implemented with proper SQLC queries
	return []VectorSearchResult{}, nil
}

// ForceSummarize immediately triggers summarization for a user regardless of threshold
func (s *MemoryService) ForceSummarize(ctx context.Context, userID, guildID string) error {
	logger.Info("Force memory summarization requested",
		zap.String("user_id", userID),
		zap.String("guild_id", guildID))

	// Get ALL buffered messages (not just up to threshold)
	messages, err := s.repo.GetMessageBufferByUserGuild(ctx, userID, guildID, 1000)
	if err != nil {
		return fmt.Errorf("failed to get buffered messages: %w", err)
	}

	if len(messages) == 0 {
		return fmt.Errorf("no messages to summarize for user %s", userID)
	}

	logger.Info("Force summarizing memory messages",
		zap.String("user_id", userID),
		zap.String("guild_id", guildID),
		zap.Int("message_count", len(messages)))

	// Create summarization request
	summarizationRequest := &SummarizationRequest{
		GuildID:         guildID,
		UserID:          userID,
		Messages:        messagesToBufferedMessages(messages),
		PreviousSummary: nil, // Force summarization creates fresh summary
		NewMessageCount: len(messages),
	}

	result, err := s.summarizer.Summarize(ctx, summarizationRequest)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	if !result.Success || result.Summary == nil {
		return fmt.Errorf("summarization failed: %v", result.Error)
	}

	// Generate embedding for the summary
	summaryEmbedding, err := s.embeddings.GenerateEmbedding(ctx, result.Summary.SummaryText)
	if err != nil {
		logger.Warn("Failed to generate force summary embedding",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Error(err))
		summaryEmbedding = nil
	}

	// Store the summary
	summary := &ConversationSummary{
		UserID:    userID,
		GuildID:   guildID,
		Content:   result.Summary.SummaryText,
		Embedding: summaryEmbedding,
		Quality:   0.8,
		CreatedAt: time.Now(),
	}

	if err := s.repo.CreateSummary(ctx, summary); err != nil {
		return fmt.Errorf("failed to store summary: %w", err)
	}

	// Clear the processed messages from buffer
	messageIDs := make([]int64, len(messages))
	for i, msg := range messages {
		messageIDs[i] = msg.ID
	}
	if err := s.repo.DeleteMessageBufferEntries(ctx, messageIDs); err != nil {
		logger.Warn("Failed to clear force-summarized memory messages",
			zap.String("user_id", userID),
			zap.String("guild_id", guildID),
			zap.Error(err))
	}

	logger.Info("Force memory summary created",
		zap.String("user_id", userID),
		zap.String("guild_id", guildID))
	return nil
}

func truncateMemoryText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}
