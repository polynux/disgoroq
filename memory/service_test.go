package memory

import (
	"context"
	"errors"
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
				ID:        summary.ID,
				UserID:    summary.UserID,
				GuildID:   summary.GuildID,
				Content:   summary.SummaryText,
				CreatedAt: summary.CreatedAt,
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
		embeddingErr    error
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
			name:         "embedding failure continues",
			userID:       "user123",
			guildID:      "guild456",
			content:      "Hello world",
			embeddingErr: errors.New("embedding failed"),
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
			embeddings.generateErr = tt.embeddingErr

			summarizer := newMockSummarizer()

			service := NewService(repo, embeddings, summarizer, DefaultServiceConfig())

			err := service.BufferMessage(context.Background(), tt.userID, tt.guildID, tt.content)

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

func TestService_SummarizationTrigger(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:    3, // Trigger after 3 messages
		SummaryInterval:    1 * time.Hour,
		MaxContextMessages: 5,
		MaxSummaryContext:  3,
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
		err := service.BufferMessage(ctx, userID, guildID, "Message "+string(rune('A'+i)))
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
	err := service.BufferMessage(ctx, userID, guildID, "Message C")
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

	// Should have summaries and recent messages
	if len(context.Summaries) == 0 {
		t.Error("expected at least one summary in context")
	}

	if len(context.RecentMessages) == 0 {
		t.Error("expected recent messages in context")
	}

	if context.ConfidenceScore < 0 || context.ConfidenceScore > 1 {
		t.Errorf("confidence score should be between 0 and 1, got %f", context.ConfidenceScore)
	}
}

func TestService_ConcurrentSummarization(t *testing.T) {
	config := ServiceConfig{
		BufferThreshold:    2,
		SummaryInterval:    100 * time.Millisecond, // Short interval for testing
		MaxContextMessages: 5,
		MaxSummaryContext:  3,
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
			err := service.BufferMessage(ctx, userID, guildID, "Concurrent message "+string(rune('A'+msgNum)))
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
		BufferThreshold:    2,
		SummaryInterval:    1 * time.Second, // 1 second interval for testing
		MaxContextMessages: 5,
		MaxSummaryContext:  3,
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
		service.BufferMessage(ctx, userID, guildID, "Message "+string(rune('A'+i)))
	}

	time.Sleep(200 * time.Millisecond) // Wait for first summarization

	// Should have 1 summary
	summaries1, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries1) != 1 {
		t.Fatalf("expected 1 summary after first batch, got %d", len(summaries1))
	}

	// Try to trigger another summarization immediately
	for i := 0; i < 2; i++ {
		service.BufferMessage(ctx, userID, guildID, "Message "+string(rune('B'+i)))
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
		service.BufferMessage(ctx, userID, guildID, "Message "+string(rune('C'+i)))
	}

	time.Sleep(200 * time.Millisecond) // Wait for third summarization

	// Should now have 2 summaries
	summaries3, _ := repo.GetSummariesByUserGuild(ctx, userID, guildID)
	if len(summaries3) != 2 {
		t.Errorf("expected 2 summaries after interval passed, got %d", len(summaries3))
	}
}
