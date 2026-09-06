package memory

import (
	"context"
	"time"
)

// Summary represents a conversation summary with embedding
type Summary struct {
	ID             int64
	GuildID        string
	UserID         string
	SummaryText    string
	MessageCount   int64
	StartMessageID string
	EndMessageID   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Embedding      []float32 // Vector embedding (768 dims for nomic-embed-text)
}

// BufferedMessage represents a message waiting to be summarized
type BufferedMessage struct {
	ID               int64
	GuildID          string
	ChannelID        string
	MessageID        string
	UserID           string
	AuthorNick       string
	Content          string
	HasImage         bool
	ImageDescription string
	Timestamp        time.Time
	Processed        bool
}

// SummarizationRequest contains data for summarization
type SummarizationRequest struct {
	GuildID         string
	UserID          string
	Messages        []BufferedMessage
	PreviousSummary *Summary
	NewMessageCount int
}

// SummarizationResult contains the result of summarization
type SummarizationResult struct {
	Summary      *Summary
	OldSummaryID *int64 // ID of previous summary if updated
	Success      bool
	Error        error
}

// VectorSearchRequest contains parameters for vector search
type VectorSearchRequest struct {
	GuildID   string
	UserID    string  // Optional: filter by specific user
	Query     string  // Search query text
	Limit     int     // Maximum results to return
	Threshold float32 // Optional: minimum similarity threshold
}

// VectorSearchResult contains a search result with similarity score
type VectorSearchResult struct {
	Summary  Summary
	Distance float32 // Cosine distance (0.0 = identical, 2.0 = opposite)
}

// MemoryStats contains statistics for a guild's memory system
type MemoryStats struct {
	TotalSummaries          int64
	UniqueUsers             int64
	TotalMessagesSummarized int64
	LastSummaryUpdate       time.Time
}

// MessageBufferEntry represents a message waiting to be summarized
type MessageBufferEntry struct {
	ID         int64
	UserID     string
	GuildID    string
	ChannelID  string
	AuthorNick string
	Content    string
	Embedding  []float32
	CreatedAt  time.Time
	MessageID  string // Optional: unique identifier for the message (auto-generated if empty)
}

// BufferMessageInput carries everything needed to buffer a Discord message
type BufferMessageInput struct {
	UserID           string
	GuildID          string
	ChannelID        string
	DiscordMessageID string
	AuthorName       string
	Content          string
	Timestamp        time.Time
}

// ConversationSummary represents a conversation summary with embedding
type ConversationSummary struct {
	ID             int64
	UserID         string
	GuildID        string
	Content        string
	MessageCount   int64
	StartMessageID string
	EndMessageID   string
	Embedding      []float32
	Quality        float64
	CreatedAt      time.Time
}

// RelevantSummary contains a summary with similarity score
type RelevantSummary struct {
	*ConversationSummary
	Similarity float64
}

// GuildMemorySettings contains per-guild memory configuration
type GuildMemorySettings struct {
	GuildID         string
	Enabled         bool
	BufferThreshold int
	SummaryInterval time.Duration
}

// MemoryContext contains summaries and recent messages for AI context
type MemoryContext struct {
	Summaries       []SummaryContext
	RecentMessages  []string
	ConfidenceScore float64
}

// SummaryContext contains a summary with metadata for AI context
type SummaryContext struct {
	Content   string
	CreatedAt time.Time
	Relevance float64
}

// Service defines the core memory service interface
type Service interface {
	// Message buffering
	BufferMessage(ctx context.Context, input BufferMessageInput) error

	// Context building
	GetMemoryContext(ctx context.Context, userID, guildID, currentMessage string) (*MemoryContext, error)

	// Search
	VectorSearch(ctx context.Context, request *VectorSearchRequest) ([]VectorSearchResult, error)

	// Management
	ClearUserMemory(ctx context.Context, userID, guildID string) error
	GetUserSummaries(ctx context.Context, userID, guildID string) ([]*ConversationSummary, error)
	GetLatestSummary(ctx context.Context, userID, guildID string) (*ConversationSummary, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildMemorySettings, error)
	UpdateGuildSettings(ctx context.Context, settings *GuildMemorySettings) error

	// Force summarization (for testing/admin)
	ForceSummarize(ctx context.Context, userID, guildID string) error
}

// EmbeddingProvider defines the interface for generating embeddings
type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
	HealthCheck(ctx context.Context) error
}

// Repository defines the database operations interface
type Repository interface {
	// Message buffer operations
	CreateMessageBufferEntry(ctx context.Context, entry *MessageBufferEntry) error
	GetMessageBufferByUserGuild(ctx context.Context, userID, guildID string, limit int) ([]*MessageBufferEntry, error)
	DeleteMessageBufferEntries(ctx context.Context, ids []int64) error
	GetRecentMessages(ctx context.Context, userID, guildID string, limit int) ([]*MessageBufferEntry, error)

	// Summary operations
	CreateSummary(ctx context.Context, summary *ConversationSummary) error
	GetLatestSummary(ctx context.Context, userID, guildID string) (*ConversationSummary, error)
	GetSummariesByUserGuild(ctx context.Context, userID, guildID string) ([]*ConversationSummary, error)
	FindRelevantSummaries(ctx context.Context, userID, guildID string, queryEmbedding []float32, limit int) ([]*RelevantSummary, error)
	ClearUserMemory(ctx context.Context, userID, guildID string) error
	UpdateSummary(ctx context.Context, summary *Summary) error

	// Guild settings
	GetGuildMemorySettings(ctx context.Context, guildID string) (*GuildMemorySettings, error)
	UpdateGuildMemorySettings(ctx context.Context, settings *GuildMemorySettings) error
}

// GuildSettingsRepository defines the interface for guild settings
type GuildSettingsRepository interface {
	GetGuildSetting(ctx context.Context, guildID, name string) (string, error)
	SetGuildSetting(ctx context.Context, guildID, name, value string) error
}
