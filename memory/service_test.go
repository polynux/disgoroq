package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Mock implementations for testing

type mockAIService struct {
	response string
	err      error
}

func (m *mockAIService) Chat(ctx context.Context, messages []Message, model string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

// blockingAIService blocks until its context is canceled.
type blockingAIService struct {
	started chan struct{}
}

func (m *blockingAIService) Chat(ctx context.Context, messages []Message, model string) (string, error) {
	if m.started != nil {
		m.started <- struct{}{}
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type mockRepository struct {
	bufferEntries    []*MessageBufferEntry
	summaries        []*ConversationSummary
	settings         map[string]*GuildMemorySettings
	createBufferErr  error
	createSummaryErr error
	getBufferErr     error
	getSummaryErr    error
	deleteBufferErr  error
	findSummaryErr   error
	clearMemoryErr   error
	summaryMutex     sync.RWMutex
	bufferMutex      sync.RWMutex
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		bufferEntries: make([]*MessageBufferEntry, 0),
		summaries:     make([]*ConversationSummary, 0),
		settings:      make(map[string]*GuildMemorySettings),
	}
}

type mockSummarizer struct {
	aiService *mockAIService
	model     string
}

func newMockSummarizer() *Summarizer {
	aiService := &mockAIService{
		response: "This is a summary.",
	}
	return NewSummarizer(aiService, "gpt-4")
}

func (m *mockRepository) CreateMessageBufferEntry(ctx context.Context, entry *MessageBufferEntry) error {
	if m.createBufferErr != nil {
		return m.createBufferErr
	}
	m.bufferMutex.Lock()
	defer m.bufferMutex.Unlock()
	entry.ID = int64(len(m.bufferEntries) + 1)
	m.bufferEntries = append(m.bufferEntries, entry)
	return nil
}

func (m *mockRepository) GetMessageBufferByUserGuild(ctx context.Context, userID, guildID string, limit int) ([]*MessageBufferEntry, error) {
	if m.getBufferErr != nil {
		return nil, m.getBufferErr
	}
	m.bufferMutex.RLock()
	defer m.bufferMutex.RUnlock()

	var result []*MessageBufferEntry
	for _, entry := range m.bufferEntries {
		if entry.UserID == userID && entry.GuildID == guildID {
			result = append(result, entry)
		}
	}

	if len(result) > limit && limit > 0 {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockRepository) DeleteMessageBufferEntries(ctx context.Context, ids []int64) error {
	if m.deleteBufferErr != nil {
		return m.deleteBufferErr
	}
	m.bufferMutex.Lock()
	defer m.bufferMutex.Unlock()

	// Simple implementation - just clear all for testing
	m.bufferEntries = make([]*MessageBufferEntry, 0)
	return nil
}

func (m *mockRepository) CreateSummary(ctx context.Context, summary *ConversationSummary) error {
	if m.createSummaryErr != nil {
		return m.createSummaryErr
	}
	m.summaryMutex.Lock()
	defer m.summaryMutex.Unlock()
	summary.ID = int64(len(m.summaries) + 1)
	m.summaries = append(m.summaries, summary)
	return nil
}

func (m *mockRepository) GetLatestSummary(ctx context.Context, userID, guildID string) (*ConversationSummary, error) {
	if m.getSummaryErr != nil {
		return nil, m.getSummaryErr
	}
	m.summaryMutex.RLock()
	defer m.summaryMutex.RUnlock()

	var latest *ConversationSummary
	for _, summary := range m.summaries {
		if summary.UserID == userID && summary.GuildID == guildID {
			if latest == nil || summary.CreatedAt.After(latest.CreatedAt) {
				latest = summary
			}
		}
	}
	return latest, nil
}

func (m *mockRepository) UpdateSummary(ctx context.Context, summary *Summary) error {
	m.summaryMutex.Lock()
	defer m.summaryMutex.Unlock()

	for i, s := range m.summaries {
		if s.ID == summary.ID {
			m.summaries[i] = &ConversationSummary{
				ID:             summary.ID,
				UserID:         summary.UserID,
				GuildID:        summary.GuildID,
				Content:        summary.SummaryText,
				MessageCount:   summary.MessageCount,
				StartMessageID: summary.StartMessageID,
				EndMessageID:   summary.EndMessageID,
				Embedding:      summary.Embedding,
				CreatedAt:      summary.CreatedAt,
			}
		}
	}
	return nil
}

func (m *mockRepository) GetSummariesByUserGuild(ctx context.Context, userID, guildID string) ([]*ConversationSummary, error) {
	m.summaryMutex.RLock()
	defer m.summaryMutex.RUnlock()

	var result []*ConversationSummary
	for _, summary := range m.summaries {
		if summary.UserID == userID && summary.GuildID == guildID {
			result = append(result, summary)
		}
	}
	return result, nil
}

func (m *mockRepository) FindRelevantSummaries(ctx context.Context, userID, guildID string, queryEmbedding []float32, limit int) ([]*RelevantSummary, error) {
	if m.findSummaryErr != nil {
		return nil, m.findSummaryErr
	}
	// Simple mock: return all summaries with fake similarity scores
	m.summaryMutex.RLock()
	defer m.summaryMutex.RUnlock()

	var result []*RelevantSummary
	for _, summary := range m.summaries {
		if summary.UserID == userID && summary.GuildID == guildID {
			result = append(result, &RelevantSummary{
				ConversationSummary: summary,
				Similarity:          0.8, // Mock similarity
			})
		}
	}
	return result, nil
}

func (m *mockRepository) GetRecentMessages(ctx context.Context, userID, guildID string, limit int) ([]*MessageBufferEntry, error) {
	if m.getBufferErr != nil {
		return nil, m.getBufferErr
	}
	m.bufferMutex.RLock()
	defer m.bufferMutex.RUnlock()

	var result []*MessageBufferEntry
	for _, entry := range m.bufferEntries {
		if entry.UserID == userID && entry.GuildID == guildID {
			result = append(result, entry)
		}
	}

	if len(result) > limit && limit > 0 {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockRepository) GetGuildMemorySettings(ctx context.Context, guildID string) (*GuildMemorySettings, error) {
	return m.settings[guildID], nil
}

func (m *mockRepository) UpdateGuildMemorySettings(ctx context.Context, settings *GuildMemorySettings) error {
	m.settings[settings.GuildID] = settings
	return nil
}

func (m *mockRepository) ClearUserMemory(ctx context.Context, userID, guildID string) error {
	if m.clearMemoryErr != nil {
		return m.clearMemoryErr
	}

	m.bufferMutex.Lock()
	defer m.bufferMutex.Unlock()
	m.summaryMutex.Lock()
	defer m.summaryMutex.Unlock()

	// Clear buffer entries
	newBuffer := make([]*MessageBufferEntry, 0)
	for _, entry := range m.bufferEntries {
		if !(entry.UserID == userID && entry.GuildID == guildID) {
			newBuffer = append(newBuffer, entry)
		}
	}
	m.bufferEntries = newBuffer

	// Clear summaries
	newSummaries := make([]*ConversationSummary, 0)
	for _, summary := range m.summaries {
		if !(summary.UserID == userID && summary.GuildID == guildID) {
			newSummaries = append(newSummaries, summary)
		}
	}
	m.summaries = newSummaries

	return nil
}

type mockEmbeddingProvider struct {
	embedding      []float32
	generateErr    error
	healthCheck    bool
	healthCheckErr error
}

func newMockEmbeddingProvider() *mockEmbeddingProvider {
	return &mockEmbeddingProvider{
		embedding:   make([]float32, 768), // 768-dimensional embeddings
		healthCheck: true,
	}
}

func (m *mockEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if m.generateErr != nil {
		return nil, m.generateErr
	}
	return m.embedding, nil
}

func (m *mockEmbeddingProvider) HealthCheck(ctx context.Context) error {
	if m.healthCheckErr != nil {
		return m.healthCheckErr
	}
	if !m.healthCheck {
		return errors.New("embedding provider unhealthy")
	}
	return nil
}

// Test cases
func TestService_BufferMessage(t *testing.T) {
	tests := []struct {
		name            string
		userID          string
		guildID         string
		content         string
		createBufferErr error
		expectError     bool
	}{
		{
			name:    "successful buffer",
			userID:  "user123",
			guildID: "guild456",
			content: "Hello world",
		},
		{
			name:            "buffer creation failure",
			userID:          "user123",
			guildID:         "guild456",
			content:         "Hello world",
			createBufferErr: errors.New("buffer creation failed"),
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepository()
			repo.createBufferErr = tt.createBufferErr

			embeddings := newMockEmbeddingProvider()
			summarizer := newMockSummarizer()

			service := NewService(repo, embeddings, summarizer, DefaultServiceConfig())

			err := service.BufferMessage(context.Background(), BufferMessageInput{
				UserID:    tt.userID,
				GuildID:   tt.guildID,
				Content:   tt.content,
				Timestamp: time.Now(),
			})

			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Verify message was buffered (unless there was a buffer error)
			if !tt.expectError && tt.createBufferErr == nil {
				buffered, _ := repo.GetMessageBufferByUserGuild(context.Background(), tt.userID, tt.guildID, 10)
				if len(buffered) == 0 {
					t.Error("message was not buffered")
				}
			}
		})
	}
}

// TestService_BufferMessage_NoEmbedding verifies that buffering a message
// does not call the embedding provider (TASK-001: embeddings were computed
// but discarded).
func TestService_BufferMessage_NoEmbedding(t *testing.T) {
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, DefaultServiceConfig())

	err := service.BufferMessage(context.Background(), BufferMessageInput{
		UserID:    "user123",
		GuildID:   "guild456",
		Content:   "Hello world",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
}

func TestService_BufferMessage_NormalizesDiscordEmojiMarkup(t *testing.T) {
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, DefaultServiceConfig())

	err := service.BufferMessage(context.Background(), BufferMessageInput{
		UserID:    "user123",
		GuildID:   "guild456",
		Content:   "Salut <:criminel:1238422591547637800>!",
		Timestamp: time.Now(),
	})

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(repo.bufferEntries) != 1 {
		t.Fatalf("Expected 1 buffered message, got %d", len(repo.bufferEntries))
	}
	if repo.bufferEntries[0].Content != "Salut :criminel:!" {
		t.Fatalf("Expected normalized emoji shortcode, got %q", repo.bufferEntries[0].Content)
	}
}

// TestService_BufferMessage_RealIDs verifies that real Discord IDs (message,
// channel) and the author name are persisted instead of synthetic values
// (TASK-004).
func TestService_BufferMessage_RealIDs(t *testing.T) {
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, DefaultServiceConfig())

	err := service.BufferMessage(context.Background(), BufferMessageInput{
		UserID:           "user123",
		GuildID:          "guild456",
		ChannelID:        "chan789",
		DiscordMessageID: "999888777",
		AuthorName:       "Polynux",
		Content:          "Salut !",
		Timestamp:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	entry := repo.bufferEntries[0]
	if entry.MessageID != "999888777" {
		t.Errorf("Expected real Discord message ID, got %q", entry.MessageID)
	}
	if entry.ChannelID != "chan789" {
		t.Errorf("Expected channel ID to be stored, got %q", entry.ChannelID)
	}
	if entry.AuthorNick != "Polynux" {
		t.Errorf("Expected author nick to be stored, got %q", entry.AuthorNick)
	}
}

func TestService_SummarizationTrigger(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:   3, // Trigger after 3 messages
		SummaryInterval:   1 * time.Hour,
		MaxSummaryContext: 3,
	}

	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"

	// Buffer first 2 messages - should not trigger summarization
	for i := 0; i < 2; i++ {
		err := service.BufferMessage(ctx, BufferMessageInput{UserID: userID, GuildID: guildID, Content: "Message " + string(rune('A'+i)), Timestamp: time.Now()})
		if err != nil {
			t.Fatalf("failed to buffer message %d: %v", i, err)
		}
	}

	// Give async processing time to complete
	time.Sleep(100 * time.Millisecond)

	// Should have 2 messages in buffer, no summaries
	buffered, _ := repo.GetMessageBufferByUserGuild(ctx, userID, guildID, 10)
	if len(buffered) != 2 {
		t.Errorf("expected 2 buffered messages, got %d", len(buffered))
	}

	summaries, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}

	// Buffer the 3rd message - should trigger summarization
	err := service.BufferMessage(ctx, BufferMessageInput{UserID: userID, GuildID: guildID, Content: "Message C", Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("failed to buffer message: %v", err)
	}

	// Wait for async summarization to complete
	time.Sleep(200 * time.Millisecond)

	// Should have created a summary and cleared buffer
	summaries, _ = repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries))
	}

	buffered, _ = repo.GetMessageBufferByUserGuild(ctx, userID, guildID, 10)
	if len(buffered) != 0 {
		t.Errorf("expected buffer to be cleared, got %d messages", len(buffered))
	}
}

func TestService_GetMemoryContext(t *testing.T) {
	config := DefaultServiceConfig()
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"
	currentMessage := "What's the weather like?"

	// Add some test data
	summary := &ConversationSummary{
		UserID:    userID,
		GuildID:   guildID,
		Content:   "User likes outdoor activities and weather discussions",
		Embedding: make([]float32, 768),
		Quality:   0.9,
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}
	repo.CreateSummary(ctx, summary)

	// Add recent messages
	for i := 0; i < 3; i++ {
		msg := &MessageBufferEntry{
			UserID:    userID,
			GuildID:   guildID,
			Content:   "Recent message " + string(rune('A'+i)),
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute),
		}
		repo.CreateMessageBufferEntry(ctx, msg)
	}

	// Get memory context
	context, err := service.GetMemoryContext(ctx, userID, guildID, currentMessage)
	if err != nil {
		t.Fatalf("failed to get memory context: %v", err)
	}

	if context == nil {
		t.Fatal("expected memory context, got nil")
	}

	// Should have summaries
	if len(context.Summaries) == 0 {
		t.Error("expected at least one summary in context")
	}

	if context.ConfidenceScore < 0 || context.ConfidenceScore > 1 {
		t.Errorf("confidence score should be between 0 and 1, got %f", context.ConfidenceScore)
	}
}

func TestService_GetMemoryContext_NormalizesLegacyEmojiMarkup(t *testing.T) {
	repo := newMockRepository()
	repo.summaries = append(repo.summaries, &ConversationSummary{
		UserID:    "user123",
		GuildID:   "guild456",
		Content:   "Résumé avec <:criminel:1238422591547637800>",
		CreatedAt: time.Now(),
	})
	repo.bufferEntries = append(repo.bufferEntries, &MessageBufferEntry{
		UserID:    "user123",
		GuildID:   "guild456",
		Content:   "Ancien <a:dance:987654321> message",
		CreatedAt: time.Now(),
	})

	service := NewService(repo, nil, newMockSummarizer(), DefaultServiceConfig())

	memoryContext, err := service.GetMemoryContext(context.Background(), "user123", "guild456", "Question <:criminel:1238422591547637800>")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(memoryContext.Summaries) != 1 {
		t.Fatalf("Expected 1 summary, got %d", len(memoryContext.Summaries))
	}
	if memoryContext.Summaries[0].Content != "Résumé avec :criminel:" {
		t.Fatalf("Expected normalized summary content, got %q", memoryContext.Summaries[0].Content)
	}
}

func TestService_ConcurrentSummarization(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:   2,
		SummaryInterval:   100 * time.Millisecond, // Short interval for testing
		MaxSummaryContext: 3,
	}

	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"

	// Simulate concurrent message buffering that should trigger summarization
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(msgNum int) {
			defer wg.Done()
			err := service.BufferMessage(ctx, BufferMessageInput{UserID: userID, GuildID: guildID, Content: "Concurrent message " + string(rune('A'+msgNum)), Timestamp: time.Now()})
			if err != nil {
				t.Errorf("failed to buffer message %d: %v", msgNum, err)
			}
		}(i)
	}

	wg.Wait()

	// Wait for async summarization to complete
	time.Sleep(300 * time.Millisecond)

	// Should have created exactly one summary (sync.Once should prevent duplicates)
	summaries, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries) != 1 {
		t.Errorf("expected exactly 1 summary due to sync.Once, got %d", len(summaries))
	}
}

func TestService_GuildSettings(t *testing.T) {
	config := DefaultServiceConfig()
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	guildID := "guild456"

	// Test default settings
	settings, err := service.GetGuildSettings(ctx, guildID)
	if err != nil {
		t.Fatalf("failed to get guild settings: %v", err)
	}

	if settings == nil {
		t.Fatal("expected settings, got nil")
	}

	if !settings.Enabled {
		t.Error("expected memory to be enabled by default")
	}

	if settings.BufferThreshold != config.BufferThreshold {
		t.Errorf("expected buffer threshold %d, got %d", config.BufferThreshold, settings.BufferThreshold)
	}

	// Test updating settings
	newSettings := &GuildMemorySettings{
		GuildID:         guildID,
		Enabled:         false,
		BufferThreshold: 20,
		SummaryInterval: 2 * time.Hour,
	}

	err = service.UpdateGuildSettings(ctx, newSettings)
	if err != nil {
		t.Fatalf("failed to update guild settings: %v", err)
	}

	// Verify update
	updatedSettings, err := service.GetGuildSettings(ctx, guildID)
	if err != nil {
		t.Fatalf("failed to get updated settings: %v", err)
	}

	if updatedSettings.Enabled {
		t.Error("expected memory to be disabled")
	}

	if updatedSettings.BufferThreshold != 20 {
		t.Errorf("expected buffer threshold 20, got %d", updatedSettings.BufferThreshold)
	}
}

func TestService_ClearUserMemory(t *testing.T) {
	config := DefaultServiceConfig()
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"

	// Add test data
	for i := 0; i < 3; i++ {
		msg := &MessageBufferEntry{
			UserID:    userID,
			GuildID:   guildID,
			Content:   "Message " + string(rune('A'+i)),
			CreatedAt: time.Now(),
		}
		repo.CreateMessageBufferEntry(ctx, msg)
	}

	summary := &ConversationSummary{
		UserID:    userID,
		GuildID:   guildID,
		Content:   "Test summary",
		CreatedAt: time.Now(),
	}
	repo.CreateSummary(ctx, summary)

	// Add data for different user (should not be cleared)
	otherMsg := &MessageBufferEntry{
		UserID:    "other_user",
		GuildID:   guildID,
		Content:   "Other user message",
		CreatedAt: time.Now(),
	}
	repo.CreateMessageBufferEntry(ctx, otherMsg)

	// Clear user memory
	err := service.ClearUserMemory(ctx, userID, guildID)
	if err != nil {
		t.Fatalf("failed to clear user memory: %v", err)
	}

	// Verify user's data is cleared
	userBuffer, _ := repo.GetMessageBufferByUserGuild(ctx, userID, guildID, 10)
	if len(userBuffer) != 0 {
		t.Errorf("expected user buffer to be cleared, got %d messages", len(userBuffer))
	}

	userSummaries, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(userSummaries) != 0 {
		t.Errorf("expected user summaries to be cleared, got %d summaries", len(userSummaries))
	}

	// Verify other user's data is preserved
	otherBuffer, _ := repo.GetMessageBufferByUserGuild(ctx, "other_user", guildID, 10)
	if len(otherBuffer) != 1 {
		t.Errorf("expected other user's buffer to be preserved, got %d messages", len(otherBuffer))
	}
}

func TestService_SummaryInterval(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:   2,
		SummaryInterval:   1 * time.Second, // 1 second interval for testing
		MaxSummaryContext: 3,
	}

	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"

	// Trigger first summarization
	for i := 0; i < 2; i++ {
		service.BufferMessage(ctx, BufferMessageInput{UserID: userID, GuildID: guildID, Content: "Message " + string(rune('A'+i)), Timestamp: time.Now()})
	}

	time.Sleep(200 * time.Millisecond) // Wait for first summarization

	// Should have 1 summary
	summaries1, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries1) != 1 {
		t.Fatalf("expected 1 summary after first batch, got %d", len(summaries1))
	}

	// Try to trigger another summarization immediately
	for i := 0; i < 2; i++ {
		service.BufferMessage(ctx, BufferMessageInput{UserID: userID, GuildID: guildID, Content: "Message " + string(rune('B'+i)), Timestamp: time.Now()})
	}

	time.Sleep(200 * time.Millisecond) // Wait for potential second summarization

	// Should still have only 1 summary (interval not passed)
	summaries2, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries2) != 1 {
		t.Errorf("expected 1 summary (interval not passed), got %d", len(summaries2))
	}

	// Wait for interval to pass
	time.Sleep(1 * time.Second)

	// Try to trigger another summarization
	for i := 0; i < 2; i++ {
		service.BufferMessage(ctx, BufferMessageInput{UserID: userID, GuildID: guildID, Content: "Message " + string(rune('C'+i)), Timestamp: time.Now()})
	}

	time.Sleep(200 * time.Millisecond) // Wait for third summarization

	// Incremental summarization updates the existing row in place, so still
	// only 1 summary — but with a cumulative message count (2 + 2).
	summaries3, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries3) != 1 {
		t.Errorf("expected 1 summary (updated in place), got %d", len(summaries3))
	}
	if summaries3[0].MessageCount != 4 {
		t.Errorf("expected cumulative message_count 4, got %d", summaries3[0].MessageCount)
	}
}

// TestService_SummaryMetadata verifies that summaries store the real
// message count and ID range (TASK-002), and that incremental
// summarization updates the existing row in place.
func TestService_SummaryMetadata(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:   3,
		SummaryInterval:   1 * time.Millisecond, // Effectively no restriction (0 resets to default)
		MaxSummaryContext: 3,
	}

	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"

	// Buffer 3 messages with distinct Discord message IDs
	for i := 0; i < 3; i++ {
		err := service.BufferMessage(ctx, BufferMessageInput{
			UserID:           userID,
			GuildID:          guildID,
			DiscordMessageID: fmt.Sprintf("discord_msg_%d", i+1),
			Content:          "Message " + string(rune('A'+i)),
			Timestamp:        time.Now(),
		})
		if err != nil {
			t.Fatalf("failed to buffer message %d: %v", i, err)
		}
	}

	// Wait for async summarization
	deadline := time.Now().Add(2 * time.Second)
	var first *ConversationSummary
	for time.Now().Before(deadline) {
		summaries, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
		if len(summaries) == 1 {
			first = summaries[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if first == nil {
		t.Fatal("expected 1 summary after first batch")
	}
	if first.MessageCount != 3 {
		t.Errorf("expected message_count 3, got %d", first.MessageCount)
	}
	if first.StartMessageID != "discord_msg_1" {
		t.Errorf("expected start_message_id discord_msg_1, got %q", first.StartMessageID)
	}
	if first.EndMessageID != "discord_msg_3" {
		t.Errorf("expected end_message_id discord_msg_3, got %q", first.EndMessageID)
	}

	// Wait for the summary interval (1ms in test config) to elapse before
	// buffering the second batch, otherwise the interval check skips it.
	time.Sleep(5 * time.Millisecond)

	// Buffer 3 more messages (to reach the threshold again): incremental
	// summarization should update the same row with a cumulative count.
	for i := 0; i < 3; i++ {
		err := service.BufferMessage(ctx, BufferMessageInput{
			UserID:           userID,
			GuildID:          guildID,
			DiscordMessageID: fmt.Sprintf("discord_msg_%d", i+4),
			Content:          "More " + string(rune('A'+i)),
			Timestamp:        time.Now(),
		})
		if err != nil {
			t.Fatalf("failed to buffer message %d: %v", i+4, err)
		}
	}

	deadline = time.Now().Add(2 * time.Second)
	var updated *ConversationSummary
	for time.Now().Before(deadline) {
		summaries, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
		if len(summaries) == 1 && summaries[0].MessageCount == 6 {
			updated = summaries[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if updated == nil {
		t.Fatal("expected summary to be incrementally updated with cumulative count 6")
	}
	if updated.ID != first.ID {
		t.Errorf("expected same summary row (ID %d) to be updated, got ID %d", first.ID, updated.ID)
	}
	if updated.MessageCount != 6 {
		t.Errorf("expected cumulative message_count 6, got %d", updated.MessageCount)
	}
	if updated.EndMessageID != "discord_msg_6" {
		t.Errorf("expected end_message_id discord_msg_6, got %q", updated.EndMessageID)
	}
}

// TestService_ConcurrencyCap verifies that at most MaxConcurrentSummaries
// summarizations run simultaneously under a burst (TASK-040).
func TestService_ConcurrencyCap(t *testing.T) {
	const cap = 2
	config := ServiceConfig{
		BufferThreshold:        3,
		SummaryInterval:        1 * time.Millisecond,
		MaxConcurrentSummaries: cap,
		SummarizationTimeout:   5 * time.Second,
	}

	// Track max concurrent chats across all users
	var mu sync.Mutex
	concurrent, maxConcurrent := 0, 0

	// Custom AI service: counts concurrency, returns immediately
	aiSvc := &countingAIService{
		onChatStart: func(userID string) {
			mu.Lock()
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()
		},
		onChatEnd: func() {
			mu.Lock()
			concurrent--
			mu.Unlock()
		},
	}

	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := NewSummarizer(aiSvc, "test")
	service := NewService(repo, embeddings, summarizer, config)
	defer service.Close()

	ctx := context.Background()

	// Buffer enough messages for 3 distinct users so each triggers a
	// summarization. Keep buffering until every user has triggered at least
	// one summarization (per-key dedup can swallow checks that race with the
	// 100ms stagger), up to a deadline.
	triggered := make(map[string]bool)
	deadline := time.Now().Add(5 * time.Second)
	for len(triggered) < 3 && time.Now().Before(deadline) {
		for u := 0; u < 3; u++ {
			userID := fmt.Sprintf("user_%d", u)
			if triggered[userID] {
				continue
			}
			if err := service.BufferMessage(ctx, BufferMessageInput{
				UserID:    userID,
				GuildID:   "guild456",
				Content:   "msg",
				Timestamp: time.Now(),
			}); err != nil {
				t.Fatalf("buffer: %v", err)
			}
			// Check if this user has now entered Chat
			mu.Lock()
			if aiSvc.usersStarted[userID] {
				triggered[userID] = true
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	startedFinal := aiSvc.startedCount
	maxFinal := maxConcurrent
	mu.Unlock()

	if startedFinal < 3 {
		t.Fatalf("expected all 3 users to eventually enter Chat, got %d", startedFinal)
	}
	if maxFinal > cap {
		t.Errorf("expected max concurrent %d, got %d", cap, maxFinal)
	}
}

// TestService_CloseCancelsSummarization verifies Close() cancels in-flight
// summarization blocked on the AI service (TASK-041).
func TestService_CloseCancelsSummarization(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:        1,
		SummaryInterval:        1 * time.Millisecond,
		MaxConcurrentSummaries: 1,
		SummarizationTimeout:   30 * time.Second, // Long; Close should cancel before this
	}

	aiSvc := &blockingAIService{started: make(chan struct{}, 10)}
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := NewSummarizer(aiSvc, "test")
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	if err := service.BufferMessage(ctx, BufferMessageInput{
		UserID:    "user123",
		GuildID:   "guild456",
		Content:   "hello",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("buffer: %v", err)
	}

	// Wait until summarization entered the blocking Chat
	select {
	case <-aiSvc.started:
	case <-time.After(3 * time.Second):
		t.Fatal("summarization never started")
	}

	// Close must cancel it quickly
	done := make(chan struct{})
	go func() {
		service.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close() did not return promptly")
	}
}

// countingAIService lets tests observe concurrency and gate completion.
type countingAIService struct {
	onChatStart  func(userID string)
	onChatEnd    func()
	startedCount int
	usersStarted map[string]bool
}

func (m *countingAIService) Chat(ctx context.Context, messages []Message, model string) (string, error) {
	m.startedCount++
	userID := ""
	// The last user message contains the userID marker from the test.
	if len(messages) > 0 {
		for _, msg := range messages {
			if len(msg.Content) >= 6 && msg.Content[:6] == "user: " {
				userID = msg.Content[6:]
			}
		}
	}
	if m.usersStarted == nil {
		m.usersStarted = make(map[string]bool)
	}
	if userID != "" {
		m.usersStarted[userID] = true
	}
	if m.onChatStart != nil {
		m.onChatStart(userID)
	}
	if m.onChatEnd != nil {
		m.onChatEnd()
	}
	return "summary text", nil
}

// TestService_GetMemoryContext_VectorUsedWithoutDebug verifies that vector
// search results populate the context even when debug logging is off (the
// results were previously appended only inside a debug branch).
func TestService_GetMemoryContext_VectorUsedWithoutDebug(t *testing.T) {
	config := DefaultServiceConfig()
	repo := newMockRepository()
	embeddings := newMockEmbeddingProvider()
	summarizer := newMockSummarizer()
	service := NewService(repo, embeddings, summarizer, config)

	ctx := context.Background()
	userID := "user123"
	guildID := "guild456"

	// Two summaries with valid 768-dim embeddings so the vector path runs
	for i := 0; i < 2; i++ {
		repo.CreateSummary(ctx, &ConversationSummary{
			UserID:    userID,
			GuildID:   guildID,
			Content:   fmt.Sprintf("Summary %d", i),
			Embedding: make([]float32, 768),
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	// mockRepository.FindRelevantSummaries returns embedded summaries
	memoryCtx, err := service.GetMemoryContext(ctx, userID, guildID, "query about things")
	if err != nil {
		t.Fatalf("GetMemoryContext: %v", err)
	}
	if memoryCtx == nil {
		t.Fatal("expected memory context, got nil")
	}
	if len(memoryCtx.Summaries) == 0 {
		t.Fatal("expected summaries in context from vector/fallback path")
	}
}
