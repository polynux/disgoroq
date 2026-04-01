package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "github.com/tursodatabase/go-libsql"
)

// TestRepository provides an in-memory SQLite database for testing
type TestRepository struct {
	db      *sql.DB
	repo    Repository
	ctx     context.Context
	cleanup func()
}

// NewTestRepository creates a test repository with in-memory database
func NewTestRepository(t *testing.T) *TestRepository {
	ctx := context.Background()

	// Use a real temp file so schema creation and prepared statements share the same DB.
	dbPath := filepath.Join(t.TempDir(), "memory-test.db")
	db, err := sql.Open("libsql", "file:"+dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Enable foreign keys and other SQLite pragmas
	_, err = db.Exec(`PRAGMA foreign_keys = ON`)
	require.NoError(t, err)

	// Create tables
	schema := `
	CREATE TABLE IF NOT EXISTS conversation_summaries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		guild_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		summary_text TEXT NOT NULL,
		message_count INTEGER NOT NULL,
		start_message_id TEXT,
		end_message_id TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		embedding BLOB
	);

	CREATE INDEX IF NOT EXISTS idx_summaries_guild_user 
	ON conversation_summaries(guild_id, user_id, updated_at DESC);

	CREATE INDEX IF NOT EXISTS idx_summaries_created 
	ON conversation_summaries(guild_id, created_at DESC);

	CREATE TABLE IF NOT EXISTS message_buffer (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		guild_id TEXT NOT NULL,
		channel_id TEXT NOT NULL,
		message_id TEXT NOT NULL UNIQUE,
		user_id TEXT NOT NULL,
		author_nick TEXT NOT NULL,
		content TEXT NOT NULL,
		has_image BOOLEAN DEFAULT 0,
		image_description TEXT,
		timestamp INTEGER NOT NULL,
		processed BOOLEAN DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_buffer_guild_user 
	ON message_buffer(guild_id, user_id, processed, timestamp ASC);

	CREATE INDEX IF NOT EXISTS idx_buffer_channel 
	ON message_buffer(channel_id, processed, timestamp ASC);

	CREATE INDEX IF NOT EXISTS idx_buffer_message_id 
	ON message_buffer(message_id);
	`

	for _, stmt := range splitTestSQL(schema) {
		_, err = db.Exec(stmt)
		require.NoError(t, err)
	}

	repo := NewRepository(db)

	cleanup := func() {
		db.Close()
	}

	return &TestRepository{
		db:      db,
		repo:    repo,
		ctx:     ctx,
		cleanup: cleanup,
	}
}

func splitTestSQL(schema string) []string {
	parts := strings.Split(schema, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		statements = append(statements, stmt)
	}
	return statements
}

// Close cleans up the test repository
func (tr *TestRepository) Close() {
	if tr.cleanup != nil {
		tr.cleanup()
	}
}

// TestMessageBufferOperations tests all message buffer operations
func TestMessageBufferOperations(t *testing.T) {
	tr := NewTestRepository(t)
	defer tr.Close()

	// Test data
	guildID := "test_guild_123"
	userID := "test_user_456"
	channelID := "test_channel_789"

	t.Run("InsertMessageBuffer", func(t *testing.T) {
		message := &MessageBufferEntry{
			ID:        1,
			UserID:    userID,
			GuildID:   guildID,
			Content:   "Hello, this is a test message",
			CreatedAt: time.Now(),
		}

		err := tr.repo.CreateMessageBufferEntry(tr.ctx, message)
		assert.NoError(t, err)
	})

	t.Run("GetUnprocessedMessageCount", func(t *testing.T) {
		messages, err := tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 100)
		assert.NoError(t, err)
		count := int64(len(messages))
		assert.Equal(t, int64(1), count)
	})

	t.Run("GetUnprocessedMessages", func(t *testing.T) {
		messages, err := tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 10)
		assert.NoError(t, err)
		assert.Len(t, messages, 1)
		assert.Equal(t, "Hello, this is a test message", messages[0].Content)
		assert.Equal(t, userID, messages[0].UserID)
	})

	t.Run("MarkMessagesAsProcessed", func(t *testing.T) {
		// Mark messages as processed by deleting them (our implementation)
		messages, err := tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 100)
		assert.NoError(t, err)

		if len(messages) > 0 {
			ids := []int64{messages[0].ID}
			err := tr.repo.DeleteMessageBufferEntries(tr.ctx, ids)
			assert.NoError(t, err)
		}

		// Verify message is processed (deleted)
		messages, err = tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 100)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(messages))
	})

	t.Run("InsertMultipleMessages", func(t *testing.T) {
		// Insert multiple messages
		for i := 0; i < 5; i++ {
			message := &BufferedMessage{
				GuildID:          guildID,
				ChannelID:        channelID,
				MessageID:        "msg_" + string(rune('0'+i+2)), // msg_002, msg_003, etc.
				UserID:           userID,
				AuthorNick:       "TestUser",
				Content:          "Message " + string(rune('0'+i+2)),
				HasImage:         i%2 == 0,
				ImageDescription: "",
				Timestamp:        time.Now().Add(time.Duration(i) * time.Second),
				Processed:        false,
			}
			err := tr.repo.CreateMessageBufferEntry(tr.ctx, &MessageBufferEntry{
				UserID:    message.UserID,
				GuildID:   message.GuildID,
				Content:   message.Content,
				CreatedAt: message.Timestamp,
			})
			assert.NoError(t, err)
		}

		messages, err := tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 100)
		assert.NoError(t, err)
		count := int64(len(messages))
		assert.Equal(t, int64(5), count)
	})

	t.Run("GetUnprocessedMessagesWithLimit", func(t *testing.T) {
		messages, err := tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 3)
		assert.NoError(t, err)
		assert.Len(t, messages, 3)

		// Should be ordered by timestamp ASC
		for i := 1; i < len(messages); i++ {
			assert.True(t, messages[i].CreatedAt.After(messages[i-1].CreatedAt) ||
				messages[i].CreatedAt.Equal(messages[i-1].CreatedAt))
		}
	})

	t.Run("DeleteProcessedMessages", func(t *testing.T) {
		messages, err := tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 100)
		assert.NoError(t, err)

		if len(messages) >= 2 {
			ids := []int64{messages[0].ID, messages[1].ID}
			err := tr.repo.DeleteMessageBufferEntries(tr.ctx, ids)
			assert.NoError(t, err)
		}

		messages, err = tr.repo.GetMessageBufferByUserGuild(tr.ctx, userID, guildID, 100)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(messages))
	})

	t.Run("DeleteMessageBufferEntries_EmptyIDs", func(t *testing.T) {
		err := tr.repo.DeleteMessageBufferEntries(tr.ctx, nil)
		assert.NoError(t, err)
	})
}

// TestSummaryOperations tests all summary operations
func TestSummaryOperations(t *testing.T) {
	tr := NewTestRepository(t)
	defer tr.Close()

	guildID := "test_guild_123"
	userID := "test_user_456"
	now := time.Now()

	t.Run("InsertSummary", func(t *testing.T) {
		conversationSummary := &ConversationSummary{
			UserID:    userID,
			GuildID:   guildID,
			Content:   "User discussed programming topics and asked about Go best practices.",
			CreatedAt: now,
		}
		err := tr.repo.CreateSummary(tr.ctx, conversationSummary)
		assert.NoError(t, err)

		retrieved, err := tr.repo.GetLatestSummary(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		assert.NotNil(t, retrieved)
		assert.Equal(t, "User discussed programming topics and asked about Go best practices.", retrieved.Content)
	})

	t.Run("GetLatestSummaryForUser", func(t *testing.T) {
		summary, err := tr.repo.GetLatestSummary(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		assert.NotNil(t, summary)
		assert.Equal(t, "User discussed programming topics and asked about Go best practices.", summary.Content)
	})

	t.Run("GetLatestSummaryForUser_NotFound", func(t *testing.T) {
		summary, err := tr.repo.GetLatestSummary(tr.ctx, "nonexistent_user", guildID)
		assert.NoError(t, err)
		assert.Nil(t, summary)
	})

	t.Run("UpdateSummary", func(t *testing.T) {
		existing, err := tr.repo.GetLatestSummary(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		assert.NotNil(t, existing)

		err = tr.repo.UpdateSummary(tr.ctx, &Summary{
			ID:           existing.ID,
			GuildID:      existing.GuildID,
			UserID:       existing.UserID,
			SummaryText:  "Updated summary: User discussed Go programming and shared code examples.",
			MessageCount: 15,
			EndMessageID: "end_msg_015",
			CreatedAt:    existing.CreatedAt,
			UpdatedAt:    now.Add(time.Hour),
			Embedding:    generateTestEmbedding(768),
		})
		assert.NoError(t, err)

		updated, err := tr.repo.GetLatestSummary(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		assert.Equal(t, "Updated summary: User discussed Go programming and shared code examples.", updated.Content)
	})

	t.Run("GetLatestSummariesForUsers", func(t *testing.T) {
		// Insert summary for another user
		userID2 := "test_user_789"
		conversationSummary2 := &ConversationSummary{
			UserID:    userID2,
			GuildID:   guildID,
			Content:   "Second user talked about Python and data science.",
			CreatedAt: now.Add(2 * time.Hour),
		}
		err := tr.repo.CreateSummary(tr.ctx, conversationSummary2)
		assert.NoError(t, err)

		// Get summaries for both users using individual calls
		summary1, err := tr.repo.GetLatestSummary(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		summary2, err := tr.repo.GetLatestSummary(tr.ctx, userID2, guildID)
		assert.NoError(t, err)

		summaries := []*ConversationSummary{summary1, summary2}
		assert.NoError(t, err)
		assert.Len(t, summaries, 2)

		// Verify we got the latest summary for each user
		assert.NotNil(t, summary1)
		assert.NotNil(t, summary2)
		assert.Equal(t, "Updated summary: User discussed Go programming and shared code examples.", summary1.Content)
		assert.Equal(t, "Second user talked about Python and data science.", summary2.Content)
	})

	t.Run("GetSummariesByGuild", func(t *testing.T) {
		// Get summaries for both users individually
		summaries1, err := tr.repo.GetSummariesByUserGuild(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		summaries2, err := tr.repo.GetSummariesByUserGuild(tr.ctx, "test_user_789", guildID)
		assert.NoError(t, err)

		totalSummaries := len(summaries1) + len(summaries2)
		assert.Equal(t, 2, totalSummaries) // We inserted 2 summaries total
	})

	t.Run("DeleteSummariesForUser", func(t *testing.T) {
		err := tr.repo.ClearUserMemory(tr.ctx, userID, guildID)
		assert.NoError(t, err)

		summary, err := tr.repo.GetLatestSummary(tr.ctx, userID, guildID)
		assert.NoError(t, err)
		assert.Nil(t, summary)

		summary2, err := tr.repo.GetLatestSummary(tr.ctx, "test_user_789", guildID)
		assert.NoError(t, err)
		assert.NotNil(t, summary2)
	})
}

// TestVectorSearchOperations tests vector search functionality
func TestVectorSearchOperations(t *testing.T) {
	tr := NewTestRepository(t)
	defer tr.Close()

	guildID := "test_guild_123"
	now := time.Now()

	// Insert multiple summaries with different embeddings
	summaries := []struct {
		userID      string
		summaryText string
		embedding   []float32
	}{
		{
			userID:      "user_go",
			summaryText: "User discussed Go programming, interfaces, and error handling.",
			embedding:   generateTestEmbeddingWithTheme(768, 0.8, 0.2, 0.1), // Programming theme
		},
		{
			userID:      "user_python",
			summaryText: "User talked about Python, pandas, and machine learning libraries.",
			embedding:   generateTestEmbeddingWithTheme(768, 0.3, 0.9, 0.4), // Data science theme
		},
		{
			userID:      "user_javascript",
			summaryText: "User discussed JavaScript, React, and frontend development.",
			embedding:   generateTestEmbeddingWithTheme(768, 0.7, 0.3, 0.8), // Web development theme
		},
	}

	for _, data := range summaries {
		conversationSummary := &ConversationSummary{
			UserID:    data.userID,
			GuildID:   guildID,
			Content:   data.summaryText,
			CreatedAt: now,
		}
		err := tr.repo.CreateSummary(tr.ctx, conversationSummary)
		require.NoError(t, err)
	}

	t.Run("VectorSearchSummaries", func(t *testing.T) {
		t.Skip("Vector search requires specialized vector database support")
	})

	t.Run("VectorSearchSummariesByUser", func(t *testing.T) {
		t.Skip("Vector search requires specialized vector database support")
	})

	t.Run("VectorSearchSummariesByUser_NoResults", func(t *testing.T) {
		t.Skip("Vector search requires specialized vector database support")
	})
}

// TestStatisticsOperations tests statistics operations
func TestStatisticsOperations(t *testing.T) {
	tr := NewTestRepository(t)
	defer tr.Close()

	guildID := "test_guild_123"
	now := time.Now()

	// Insert multiple summaries
	userIDs := []string{"user1", "user2", "user3"}
	for i, userID := range userIDs {
		conversationSummary := &ConversationSummary{
			UserID:    userID,
			GuildID:   guildID,
			Content:   fmt.Sprintf("Summary %d for stats testing", i),
			CreatedAt: now.Add(time.Duration(i) * time.Hour),
		}
		err := tr.repo.CreateSummary(tr.ctx, conversationSummary)
		require.NoError(t, err)
	}

	t.Run("GetSummaryStatsByGuild", func(t *testing.T) {
		summaries, err := tr.repo.GetSummariesByUserGuild(tr.ctx, "user1", guildID)
		assert.NoError(t, err)
		assert.NotNil(t, summaries)
		assert.Equal(t, 1, len(summaries))

		// Test with another user to verify isolation
		summaries2, err := tr.repo.GetSummariesByUserGuild(tr.ctx, "user2", guildID)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(summaries2))
	})

	t.Run("GetSummaryStatsByGuild_Empty", func(t *testing.T) {
		summaries, err := tr.repo.GetSummariesByUserGuild(tr.ctx, "nonexistent_user", "empty_guild")
		assert.NoError(t, err)
		assert.Nil(t, summaries)
		assert.Equal(t, 0, len(summaries))
	})
}

// TestEmbeddingConversion tests F32_BLOB conversion functions
func TestEmbeddingConversion(t *testing.T) {
	t.Run("float32SliceToF32Blob_Valid", func(t *testing.T) {
		embedding := generateTestEmbedding(768)
		blob, err := float32SliceToF32Blob(embedding)
		assert.NoError(t, err)
		assert.NotNil(t, blob)
		assert.Equal(t, 3075, len(blob)) // 1 + 2 + (768 * 4)

		// Verify header
		assert.Equal(t, byte(0x00), blob[0])
		assert.Equal(t, uint16(768), binary.LittleEndian.Uint16(blob[1:3]))
	})

	t.Run("float32SliceToF32Blob_Empty", func(t *testing.T) {
		blob, err := float32SliceToF32Blob([]float32{})
		assert.NoError(t, err)
		assert.Nil(t, blob)
	})

	t.Run("float32SliceToF32Blob_WrongDimensions", func(t *testing.T) {
		embedding := generateTestEmbedding(384) // Wrong size
		blob, err := float32SliceToF32Blob(embedding)
		assert.Error(t, err)
		assert.Nil(t, blob)
		assert.Contains(t, err.Error(), "expected 768 dimensions")
	})

	t.Run("f32BlobToFloat32Slice_Valid", func(t *testing.T) {
		original := generateTestEmbedding(768)
		blob, err := float32SliceToF32Blob(original)
		require.NoError(t, err)

		converted, err := f32BlobToFloat32Slice(blob)
		assert.NoError(t, err)
		assert.Equal(t, len(original), len(converted))

		// Values should be approximately equal (allowing for floating point precision)
		for i := 0; i < len(original); i++ {
			assert.InDelta(t, original[i], converted[i], 0.0001)
		}
	})

	t.Run("f32BlobToFloat32Slice_Nil", func(t *testing.T) {
		result, err := f32BlobToFloat32Slice(nil)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("f32BlobToFloat32Slice_InvalidBlob", func(t *testing.T) {
		invalidBlob := []byte{0x01, 0x02} // Too small
		result, err := f32BlobToFloat32Slice(invalidBlob)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "blob too small")
	})
}

// TestEmbeddingMath tests embedding mathematical operations
func TestEmbeddingMath(t *testing.T) {
	t.Run("CosineSimilarity_Identical", func(t *testing.T) {
		embedding := generateTestEmbedding(768)
		similarity, err := CosineSimilarity(embedding, embedding)
		assert.NoError(t, err)
		assert.InDelta(t, 1.0, similarity, 0.0001)
	})

	t.Run("CosineSimilarity_Orthogonal", func(t *testing.T) {
		// Create orthogonal embeddings
		embedding1 := make([]float32, 768)
		embedding2 := make([]float32, 768)
		embedding1[0] = 1.0
		embedding2[1] = 1.0

		similarity, err := CosineSimilarity(embedding1, embedding2)
		assert.NoError(t, err)
		assert.InDelta(t, 0.0, similarity, 0.0001)
	})

	t.Run("CosineDistance", func(t *testing.T) {
		embedding1 := generateTestEmbedding(768)
		embedding2 := generateTestEmbedding(768)

		similarity, err := CosineSimilarity(embedding1, embedding2)
		require.NoError(t, err)

		distance, err := CosineDistance(embedding1, embedding2)
		require.NoError(t, err)

		assert.InDelta(t, 1-similarity, distance, 0.0001)
	})

	t.Run("NormalizeEmbedding", func(t *testing.T) {
		embedding := generateTestEmbedding(768)
		normalized := NormalizeEmbedding(embedding)

		// Check that normalized embedding has unit length
		var norm float32
		for _, val := range normalized {
			norm += val * val
		}
		norm = float32(math.Sqrt(float64(norm)))
		assert.InDelta(t, 1.0, norm, 0.0001)
	})

	t.Run("ValidateEmbedding_Valid", func(t *testing.T) {
		embedding := generateTestEmbedding(768)
		err := ValidateEmbedding(embedding)
		assert.NoError(t, err)
	})

	t.Run("ValidateEmbedding_InvalidDimensions", func(t *testing.T) {
		embedding := generateTestEmbedding(384)
		err := ValidateEmbedding(embedding)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected 768 dimensions")
	})

	t.Run("ValidateEmbedding_NaN", func(t *testing.T) {
		embedding := generateTestEmbedding(768)
		embedding[100] = float32(math.NaN())
		err := ValidateEmbedding(embedding)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid value")
	})
}

// Helper functions for generating test data

func generateTestEmbedding(dimensions int) []float32 {
	embedding := make([]float32, dimensions)
	for i := 0; i < dimensions; i++ {
		embedding[i] = float32(i%100) / 100.0 // Simple pattern for reproducibility
	}
	return embedding
}

func generateTestEmbeddingWithTheme(dimensions int, programming, dataScience, webDev float32) []float32 {
	embedding := make([]float32, dimensions)

	// Create a themed embedding by setting different patterns
	for i := 0; i < dimensions; i++ {
		base := float32(i%100) / 100.0

		// Add theme-specific patterns
		if i < dimensions/3 {
			embedding[i] = base*programming + (float32(i%10) / 50.0)
		} else if i < 2*dimensions/3 {
			embedding[i] = base*dataScience + (float32(i%15) / 75.0)
		} else {
			embedding[i] = base*webDev + (float32(i%20) / 100.0)
		}
	}

	return embedding
}
