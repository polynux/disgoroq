package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// isDebugMode checks if LOG_LEVEL is set to debug
func isDebugMode() bool {
	return strings.ToLower(os.Getenv("LOG_LEVEL")) == "debug"
}

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
func (s *MemoryService) BufferMessage(ctx context.Context, userID, guildID, content string) error {
	if isDebugMode() {
		log.Printf("[Memory] Buffering message for user %s in guild %s", userID, guildID)
	}

	// Create embedding for the message
	embedding, err := s.embeddings.GenerateEmbedding(ctx, content)
	if err != nil {
		// Log error but continue without embedding
		if isDebugMode() {
			log.Printf("[Memory] Failed to generate embedding for message: %v", err)
		}
		embedding = nil
	}

	// Generate unique message ID using multiple entropy sources
	messageID := fmt.Sprintf("msg_%d_%s_%d", time.Now().UnixNano(), userID, time.Now().Nanosecond())

	// Store the message in buffer
	message := &MessageBufferEntry{
		UserID:    userID,
		GuildID:   guildID,
		Content:   content,
		Embedding: embedding,
		CreatedAt: time.Now(),
		MessageID: messageID,
	}

	if err := s.repo.CreateMessageBufferEntry(ctx, message); err != nil {
		return fmt.Errorf("[Memory] failed to buffer message: %w", err)
	}

	if isDebugMode() {
		log.Printf("[Memory] Message buffered successfully for user %s (ID: %s)", userID, messageID)
	}

	// Check if we should trigger summarization
	go s.checkAndSummarizeAsync(userID, guildID)

	return nil
}

// checkAndSummarizeAsync runs summarization check in background
func (s *MemoryService) checkAndSummarizeAsync(userID, guildID string) {
	key := fmt.Sprintf("%s:%s", userID, guildID)

	// Check if summarization is already running for this user/guild
	if _, loaded := s.activeSummaries.LoadOrStore(key, true); loaded {
		if isDebugMode() {
			log.Printf("[Memory] Summarization already running for user %s in guild %s, skipping", userID, guildID)
		}
		return
	}

	// Clean up after summarization completes
	defer s.activeSummaries.Delete(key)

	// Small delay to let main transaction complete
	time.Sleep(100 * time.Millisecond)

	ctx := context.Background()
	if isDebugMode() {
		log.Printf("[Memory] Checking if summarization needed for user %s in guild %s", userID, guildID)
	}
	if err := s.runSummarization(ctx, userID, guildID); err != nil {
		log.Printf("[Memory] Summarization failed for user %s in guild %s: %v", userID, guildID, err)
	}
}

// runSummarization performs the actual summarization
func (s *MemoryService) runSummarization(ctx context.Context, userID, guildID string) error {
	if isDebugMode() {
		log.Printf("[Memory] Running summarization check for user %s in guild %s", userID, guildID)
	}

	// Check if enough time has passed since last summary
	lastSummary, err := s.repo.GetLatestSummary(ctx, userID, guildID)
	if err == nil && lastSummary != nil {
		timeSinceLast := time.Since(lastSummary.CreatedAt)
		if timeSinceLast < s.summaryInterval {
			if isDebugMode() {
				log.Printf("[Memory] Too soon for another summary (last was %v ago, need %v)", timeSinceLast, s.summaryInterval)
			}
			return nil // Too soon for another summary
		}
	}

	// Get buffered messages
	messages, err := s.repo.GetMessageBufferByUserGuild(ctx, userID, guildID, s.bufferThreshold)
	if err != nil {
		return fmt.Errorf("failed to get buffered messages: %w", err)
	}

	if isDebugMode() {
		log.Printf("[Memory] Found %d buffered messages for user %s (threshold: %d)", len(messages), userID, s.bufferThreshold)
	}

	if len(messages) < s.bufferThreshold {
		if isDebugMode() {
			log.Printf("[Memory] Not enough messages to summarize (%d/%d)", len(messages), s.bufferThreshold)
		}
		return nil // Not enough messages yet
	}

	log.Printf("[Memory] Triggering summarization for user %s with %d messages", userID, len(messages))

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
		log.Printf("Failed to generate embedding for summary: %v", err)
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
		log.Printf("Failed to clear processed messages: %v", err)
	}

	log.Printf("Created summary for user %s in guild %s (quality: %.2f)", userID, guildID, 0.8)
	return nil
}

// Helper functions to convert between types
func messagesToBufferedMessages(messages []*MessageBufferEntry) []BufferedMessage {
	result := make([]BufferedMessage, len(messages))
	for i, msg := range messages {
		result[i] = BufferedMessage{
			ID:        msg.ID,
			GuildID:   msg.GuildID,
			UserID:    msg.UserID,
			Content:   msg.Content,
			Timestamp: msg.CreatedAt,
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
		conversation += fmt.Sprintf("[%s] %s\n", msg.CreatedAt.Format("15:04"), msg.Content)
	}
	return conversation
}

// GetMemoryContext builds context for AI responses
func (s *MemoryService) GetMemoryContext(ctx context.Context, userID, guildID, currentMessage string) (*MemoryContext, error) {
	if isDebugMode() {
		log.Printf("[Memory] Building context for user %s, message: %.30s...", userID, currentMessage)
	}

	context := &MemoryContext{
		Summaries:       []SummaryContext{},
		RecentMessages:  []string{},
		ConfidenceScore: 0.0,
	}

	// First, get ALL summaries for this user (fallback if vector search fails)
	allSummaries, err := s.repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if err != nil {
		if isDebugMode() {
			log.Printf("[Memory] Error getting user summaries: %v", err)
		}
	} else if isDebugMode() {
		log.Printf("[Memory] Found %d total summaries for user %s", len(allSummaries), userID)
	}

	// Get relevant summaries using vector similarity
	if s.embeddings != nil {
		queryEmbedding, err := s.embeddings.GenerateEmbedding(ctx, currentMessage)
		if err != nil {
			if isDebugMode() {
				log.Printf("[Memory] Failed to generate query embedding: %v", err)
			}
		} else if queryEmbedding != nil {
			if isDebugMode() {
				log.Printf("[Memory] Generated embedding for query, searching for relevant summaries...")
			}
			summaries, err := s.repo.FindRelevantSummaries(ctx, userID, guildID, queryEmbedding, s.maxSummaryContext)
			if err != nil {
				if isDebugMode() {
					log.Printf("[Memory] Vector search failed: %v", err)
				}
			} else if isDebugMode() {
				log.Printf("[Memory] Vector search returned %d summaries", len(summaries))
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
		log.Printf("[Memory] No embedding provider available")
	}

	// Fallback: if vector search returned nothing but we have summaries, use the most recent one
	if len(context.Summaries) == 0 && len(allSummaries) > 0 {
		log.Printf("[Memory] Vector search returned no results, using most recent summary as fallback")
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
			context.RecentMessages = append(context.RecentMessages, msg.Content)
		}
	}

	// Calculate confidence score based on summary relevance
	if len(context.Summaries) > 0 {
		totalRelevance := 0.0
		for _, summary := range context.Summaries {
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
	log.Printf("[Memory] Force summarization requested for user %s in guild %s", userID, guildID)

	// Get ALL buffered messages (not just up to threshold)
	messages, err := s.repo.GetMessageBufferByUserGuild(ctx, userID, guildID, 1000)
	if err != nil {
		return fmt.Errorf("failed to get buffered messages: %w", err)
	}

	if len(messages) == 0 {
		return fmt.Errorf("no messages to summarize for user %s", userID)
	}

	log.Printf("[Memory] Force summarizing %d messages for user %s", len(messages), userID)

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
		log.Printf("[Memory] Failed to generate embedding for summary: %v", err)
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
		log.Printf("[Memory] Failed to clear processed messages: %v", err)
	}

	log.Printf("[Memory] Force summary created for user %s in guild %s", userID, guildID)
	return nil
}
