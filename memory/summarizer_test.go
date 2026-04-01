package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAIService implements the AIService interface for testing
type MockAIService struct {
	mock.Mock
}

func (m *MockAIService) Chat(ctx context.Context, messages []Message, model string) (string, error) {
	args := m.Called(ctx, messages, model)
	return args.String(0), args.Error(1)
}

// TestSummarizer tests the AI summarization functionality
func TestSummarizer(t *testing.T) {
	t.Run("NewSummarizer", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "")
		assert.NotNil(t, summarizer)
		assert.Equal(t, "llama3-8b-8192", summarizer.model)
		assert.Equal(t, mockAI, summarizer.aiService)
	})

	t.Run("NewSummarizer_CustomModel", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "custom-model")
		assert.NotNil(t, summarizer)
		assert.Equal(t, "custom-model", summarizer.model)
	})
}

// TestSummarizer_Summarize tests the main summarization functionality
func TestSummarizer_Summarize(t *testing.T) {
	t.Run("SuccessfulSummarization", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "test-model")

		messages := []BufferedMessage{
			{
				MessageID:  "msg1",
				AuthorNick: "Alice",
				Content:    "I love programming in Go!",
				Timestamp:  time.Now(),
			},
			{
				MessageID:  "msg2",
				AuthorNick: "Bob",
				Content:    "Go is really efficient for concurrent programming.",
				Timestamp:  time.Now(),
			},
		}

		request := &SummarizationRequest{
			GuildID:         "test_guild",
			UserID:          "test_user",
			Messages:        messages,
			PreviousSummary: nil,
			NewMessageCount: len(messages),
		}

		expectedSummary := "User discussed Go programming and its efficiency for concurrent tasks."

		mockAI.On("Chat", mock.Anything, mock.Anything, "test-model").Return(expectedSummary, nil)

		result, err := summarizer.Summarize(context.Background(), request)
		assert.NoError(t, err)
		assert.True(t, result.Success)
		assert.Nil(t, result.Error)
		assert.NotNil(t, result.Summary)
		assert.Equal(t, expectedSummary, result.Summary.SummaryText)
		assert.Equal(t, int64(2), result.Summary.MessageCount)
		assert.Equal(t, "msg1", result.Summary.StartMessageID)
		assert.Equal(t, "msg2", result.Summary.EndMessageID)
		assert.Equal(t, "test_guild", result.Summary.GuildID)
		assert.Equal(t, "test_user", result.Summary.UserID)

		mockAI.AssertExpectations(t)
	})

	t.Run("IncrementalSummarization", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "test-model")

		previousSummary := &Summary{
			ID:             1,
			GuildID:        "test_guild",
			UserID:         "test_user",
			SummaryText:    "Previously discussed Python basics.",
			MessageCount:   5,
			StartMessageID: "old_start",
			EndMessageID:   "old_end",
			CreatedAt:      time.Now().Add(-1 * time.Hour),
			UpdatedAt:      time.Now().Add(-1 * time.Hour),
		}

		newMessages := []BufferedMessage{
			{
				MessageID:  "msg3",
				AuthorNick: "Alice",
				Content:    "Now I'm learning about Go interfaces!",
				Timestamp:  time.Now(),
			},
		}

		request := &SummarizationRequest{
			GuildID:         "test_guild",
			UserID:          "test_user",
			Messages:        newMessages,
			PreviousSummary: previousSummary,
			NewMessageCount: len(newMessages),
		}

		expectedSummary := "User learned Python basics and is now studying Go interfaces."

		mockAI.On("Chat", mock.Anything, mock.Anything, "test-model").Return(expectedSummary, nil)

		result, err := summarizer.Summarize(context.Background(), request)
		assert.NoError(t, err)
		assert.True(t, result.Success)
		assert.Equal(t, expectedSummary, result.Summary.SummaryText)
		assert.Equal(t, int64(6), result.Summary.MessageCount)
		assert.Equal(t, "old_start", result.Summary.StartMessageID)
		assert.Equal(t, "msg3", result.Summary.EndMessageID)

		mockAI.AssertExpectations(t)
	})

	t.Run("EmptyMessages", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "test-model")

		request := &SummarizationRequest{
			GuildID:         "test_guild",
			UserID:          "test_user",
			Messages:        []BufferedMessage{},
			PreviousSummary: nil,
			NewMessageCount: 0,
		}

		result, err := summarizer.Summarize(context.Background(), request)
		assert.NoError(t, err)
		assert.False(t, result.Success)
		assert.NotNil(t, result.Error)
		assert.Contains(t, result.Error.Error(), "no messages to summarize")
		assert.Nil(t, result.Summary)
	})

	t.Run("AIServiceError", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "test-model")

		messages := []BufferedMessage{
			{
				MessageID:  "msg1",
				AuthorNick: "Alice",
				Content:    "Test message",
				Timestamp:  time.Now(),
			},
		}

		request := &SummarizationRequest{
			GuildID:         "test_guild",
			UserID:          "test_user",
			Messages:        messages,
			PreviousSummary: nil,
			NewMessageCount: len(messages),
		}

		expectedError := errors.New("AI service failed")
		mockAI.On("Chat", mock.Anything, mock.Anything, "test-model").Return("", expectedError)

		result, err := summarizer.Summarize(context.Background(), request)
		assert.NoError(t, err)
		assert.False(t, result.Success)
		assert.NotNil(t, result.Error)
		assert.Contains(t, result.Error.Error(), "AI service error")
		assert.Nil(t, result.Summary)

		mockAI.AssertExpectations(t)
	})

	t.Run("EmptySummary", func(t *testing.T) {
		mockAI := new(MockAIService)
		summarizer := NewSummarizer(mockAI, "test-model")

		messages := []BufferedMessage{
			{
				MessageID:  "msg1",
				AuthorNick: "Alice",
				Content:    "Test message",
				Timestamp:  time.Now(),
			},
		}

		request := &SummarizationRequest{
			GuildID:         "test_guild",
			UserID:          "test_user",
			Messages:        messages,
			PreviousSummary: nil,
			NewMessageCount: len(messages),
		}

		mockAI.On("Chat", mock.Anything, mock.Anything, "test-model").Return("", nil)

		result, err := summarizer.Summarize(context.Background(), request)
		assert.NoError(t, err)
		assert.False(t, result.Success)
		assert.NotNil(t, result.Error)
		assert.Contains(t, result.Error.Error(), "AI returned empty summary")
		assert.Nil(t, result.Summary)

		mockAI.AssertExpectations(t)
	})
}

// TestSummarizer_buildPrompt tests prompt construction
func TestSummarizer_buildPrompt(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	t.Run("BasicPrompt", func(t *testing.T) {
		messages := []BufferedMessage{
			{
				AuthorNick: "Alice",
				Content:    "I love Go programming!",
			},
			{
				AuthorNick: "Bob",
				Content:    "Me too! It's so efficient.",
			},
		}

		request := &SummarizationRequest{
			Messages:        messages,
			NewMessageCount: len(messages),
		}

		prompt := summarizer.buildPrompt(request)

		assert.Contains(t, prompt, "Create a brief summary of this conversation")
		assert.Contains(t, prompt, "IMPORTANT RULES:")
		assert.Contains(t, prompt, "Focus on: topics, user interests, key info, communication style")
		assert.Contains(t, prompt, "CONVERSATION:")
		assert.Contains(t, prompt, "Alice: I love Go programming!")
		assert.Contains(t, prompt, "Bob: Me too! It's so efficient.")
		assert.Contains(t, prompt, "YOUR SUMMARY (direct output, no prefix):")
	})

	t.Run("PromptWithPreviousSummary", func(t *testing.T) {
		previousSummary := &Summary{
			SummaryText: "Previously discussed Python basics.",
		}

		messages := []BufferedMessage{
			{
				AuthorNick: "Alice",
				Content:    "Now learning Go interfaces!",
			},
		}

		request := &SummarizationRequest{
			Messages:        messages,
			PreviousSummary: previousSummary,
			NewMessageCount: len(messages),
		}

		prompt := summarizer.buildPrompt(request)

		assert.Contains(t, prompt, "PREVIOUS CONTEXT: Previously discussed Python basics.")
		assert.Contains(t, prompt, "NEW MESSAGES:")
		assert.Contains(t, prompt, "Alice: Now learning Go interfaces!")
	})
}

// TestSummarizer_cleanSummary tests summary cleaning
func TestSummarizer_cleanSummary(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "RemoveSummaryPrefix",
			input:    "Summary: This is a great conversation about Go.",
			expected: "This is a great conversation about Go.",
		},
		{
			name:     "RemoveHeresSummaryPrefix",
			input:    "Here's a summary: This is a great conversation about Go.",
			expected: "This is a great conversation about Go.",
		},
		{
			name:     "RemoveBasedOnPrefix",
			input:    "Based on the conversation: This is a great conversation about Go.",
			expected: "This is a great conversation about Go.",
		},
		{
			name:     "TrimWhitespace",
			input:    "  This is a clean summary.  ",
			expected: "This is a clean summary.",
		},
		{
			name:     "LongSummaryTruncated",
			input:    strings.Repeat("This is a very long sentence that should be truncated.", 20),
			expected: strings.Repeat("This is a very long sentence that should be truncated.", 10) + "...",
		},
		{
			name:     "EmptyInput",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizer.cleanSummary(tt.input)
			if tt.name == "LongSummaryTruncated" {
				assert.NotEmpty(t, result)
				assert.Less(t, len(result), len(tt.input))
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestSummarizer_ValidateSummary tests summary validation
func TestSummarizer_ValidateSummary(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	tests := []struct {
		name      string
		summary   string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "ValidSummary",
			summary:   "This is a valid summary that discusses Go programming and user interests.",
			wantError: false,
		},
		{
			name:      "EmptySummary",
			summary:   "",
			wantError: true,
			errorMsg:  "summary is empty",
		},
		{
			name:      "TooShort",
			summary:   "Short",
			wantError: true,
			errorMsg:  "summary too short",
		},
		{
			name:      "TooLong",
			summary:   strings.Repeat("This is a very long summary ", 50),
			wantError: true,
			errorMsg:  "summary too long",
		},
		{
			name:      "ExcessiveRepetition",
			summary:   "This this this this is is is is a a a a summary summary summary summary.",
			wantError: true,
			errorMsg:  "excessive repetition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := summarizer.ValidateSummary(tt.summary)
			if tt.wantError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSummarizer_EstimateSummaryQuality tests quality scoring
func TestSummarizer_EstimateSummaryQuality(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	tests := []struct {
		name     string
		summary  string
		minScore int
		maxScore int
	}{
		{
			name:     "EmptySummary",
			summary:  "",
			minScore: 0,
			maxScore: 0,
		},
		{
			name:     "VeryShortSummary",
			summary:  "Hi",
			minScore: 0,
			maxScore: 50,
		},
		{
			name:     "OptimalLengthSummary",
			summary:  "This is a well-written summary that discusses programming topics and user interests. It has good content diversity and covers the main points effectively.",
			minScore: 70,
			maxScore: 100,
		},
		{
			name:     "VeryLongSummary",
			summary:  strings.Repeat("This is a very long summary that goes on and on about various topics including programming, user interests, and many other things. ", 20),
			minScore: 0,
			maxScore: 90,
		},
		{
			name:     "RepetitiveSummary",
			summary:  "This is a summary. This is a summary. This is a summary. This is a summary.",
			minScore: 0,
			maxScore: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := summarizer.EstimateSummaryQuality(tt.summary)
			assert.GreaterOrEqual(t, score, tt.minScore)
			assert.LessOrEqual(t, score, tt.maxScore)
			assert.GreaterOrEqual(t, score, 0)
			assert.LessOrEqual(t, score, 100)
		})
	}
}

// TestSummarizer_ExtractKeyTopics tests keyword extraction
func TestSummarizer_ExtractKeyTopics(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	messages := []BufferedMessage{
		{
			AuthorNick: "Alice",
			Content:    "I love programming in Go! Go is so efficient.",
		},
		{
			AuthorNick: "Bob",
			Content:    "Python is great for data science and machine learning.",
		},
		{
			AuthorNick: "Charlie",
			Content:    "JavaScript is essential for web development.",
		},
	}

	topics := summarizer.ExtractKeyTopics(messages)

	assert.NotEmpty(t, topics)
	assert.LessOrEqual(t, len(topics), 10)

	for _, topic := range topics {
		_ = topic
	}

	assert.NotEmpty(t, topics)
}

// TestSummarizer_FormatMessagesForPrompt tests message formatting
func TestSummarizer_FormatMessagesForPrompt(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	messages := []BufferedMessage{
		{
			AuthorNick: "Alice",
			Content:    "Hello everyone!",
			Timestamp:  time.Date(2024, 1, 1, 10, 30, 0, 0, time.UTC),
		},
		{
			AuthorNick: "Bob",
			Content:    "Hi Alice!",
			Timestamp:  time.Date(2024, 1, 1, 10, 31, 0, 0, time.UTC),
		},
	}

	formatted := summarizer.FormatMessagesForPrompt(messages)

	assert.Contains(t, formatted, "[10:30] Alice: Hello everyone!")
	assert.Contains(t, formatted, "[10:31] Bob: Hi Alice!")
}

// TestSummarizer_IsSummaryStale tests staleness checking
func TestSummarizer_IsSummaryStale(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	t.Run("FreshSummary", func(t *testing.T) {
		summary := &Summary{
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		}
		assert.False(t, summarizer.IsSummaryStale(summary, 24*time.Hour))
	})

	t.Run("StaleSummary", func(t *testing.T) {
		summary := &Summary{
			UpdatedAt: time.Now().Add(-25 * time.Hour),
		}
		assert.True(t, summarizer.IsSummaryStale(summary, 24*time.Hour))
	})

	t.Run("NilSummary", func(t *testing.T) {
		assert.True(t, summarizer.IsSummaryStale(nil, 24*time.Hour))
	})
}

// TestSummarizer_MergeSummaries tests summary merging
func TestSummarizer_MergeSummaries(t *testing.T) {
	summarizer := &Summarizer{model: "test-model"}

	t.Run("SingleSummary", func(t *testing.T) {
		summaries := []Summary{
			{SummaryText: "User discussed Go programming."},
		}

		merged, err := summarizer.MergeSummaries(summaries)
		assert.NoError(t, err)
		assert.Equal(t, "User discussed Go programming.", merged)
	})

	t.Run("MultipleSummaries", func(t *testing.T) {
		summaries := []Summary{
			{SummaryText: "User learned Python basics."},
			{SummaryText: "User studied Go interfaces."},
			{SummaryText: "User built a web app."},
		}

		merged, err := summarizer.MergeSummaries(summaries)
		assert.NoError(t, err)
		assert.Contains(t, merged, "Combined conversation summary:")
		assert.Contains(t, merged, "Period 1: User learned Python basics.")
		assert.Contains(t, merged, "Period 2: User studied Go interfaces.")
		assert.Contains(t, merged, "Period 3: User built a web app.")
	})

	t.Run("EmptySummaries", func(t *testing.T) {
		summaries := []Summary{}

		merged, err := summarizer.MergeSummaries(summaries)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no summaries to merge")
		assert.Equal(t, "", merged)
	})
}

// TestSummarizer_RealisticConversation tests with realistic conversation data
func TestSummarizer_RealisticConversation(t *testing.T) {
	mockAI := new(MockAIService)
	summarizer := NewSummarizer(mockAI, "test-model")

	messages := []BufferedMessage{
		{
			MessageID:  "msg1",
			AuthorNick: "User123",
			Content:    "Hey everyone! I'm new to Go and trying to understand interfaces. Can someone explain them to me?",
			Timestamp:  time.Now(),
		},
		{
			MessageID:  "msg2",
			AuthorNick: "GoExpert",
			Content:    "Welcome! Interfaces in Go are a way to define behavior. Think of them as contracts that types can implement.",
			Timestamp:  time.Now(),
		},
		{
			MessageID:  "msg3",
			AuthorNick: "User123",
			Content:    "That makes sense! So if I have a Reader interface, anything that implements Read() can be used as a Reader?",
			Timestamp:  time.Now(),
		},
		{
			MessageID:  "msg4",
			AuthorNick: "GoExpert",
			Content:    "Exactly! That's the power of interfaces. You can write functions that work with any type that satisfies the interface.",
			Timestamp:  time.Now(),
		},
		{
			MessageID:  "msg5",
			AuthorNick: "DevHelper",
			Content:    "Don't forget about the empty interface interface{} - it can hold any type!",
			Timestamp:  time.Now(),
		},
	}

	request := &SummarizationRequest{
		GuildID:         "golang_help",
		UserID:          "user123",
		Messages:        messages,
		PreviousSummary: nil,
		NewMessageCount: len(messages),
	}

	expectedSummary := "New user asked about Go interfaces and received helpful explanations about interface contracts and the empty interface."

	mockAI.On("Chat", mock.Anything, mock.Anything, "test-model").Return(expectedSummary, nil)

	result, err := summarizer.Summarize(context.Background(), request)
	assert.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, expectedSummary, result.Summary.SummaryText)
	assert.Equal(t, int64(5), result.Summary.MessageCount)
	assert.Equal(t, "msg1", result.Summary.StartMessageID)
	assert.Equal(t, "msg5", result.Summary.EndMessageID)

	mockAI.AssertExpectations(t)

	call := mockAI.Calls[0]
	messagesArg := call.Arguments[1].([]Message)

	assert.Len(t, messagesArg, 2)
	assert.Equal(t, "system", messagesArg[0].Role)
	assert.Equal(t, "user", messagesArg[1].Role)

	userPrompt := messagesArg[1].Content
	assert.Contains(t, userPrompt, "User123: Hey everyone! I'm new to Go")
	assert.Contains(t, userPrompt, "GoExpert: Welcome! Interfaces in Go")
}

// TestSummarizer_PromptConstruction tests the detailed prompt construction
func TestSummarizer_PromptConstruction(t *testing.T) {
	mockAI := new(MockAIService)
	summarizer := NewSummarizer(mockAI, "test-model")

	previousSummary := &Summary{
		SummaryText: "User previously asked about Python basics and data structures.",
	}

	newMessages := []BufferedMessage{
		{
			MessageID:  "msg1",
			AuthorNick: "User123",
			Content:    "Now I'm curious about Go! What makes it special compared to Python?",
			Timestamp:  time.Now(),
		},
		{
			MessageID:  "msg2",
			AuthorNick: "GoExpert",
			Content:    "Go excels at concurrency with goroutines and channels. It's compiled and has great performance!",
			Timestamp:  time.Now(),
		},
	}

	request := &SummarizationRequest{
		GuildID:         "programming_chat",
		UserID:          "user123",
		Messages:        newMessages,
		PreviousSummary: previousSummary,
		NewMessageCount: len(newMessages),
	}

	expectedSummary := "User transitioned from Python to learning Go, showing interest in concurrency features like goroutines and channels."

	var capturedPrompt string
	mockAI.On("Chat", mock.Anything, mock.Anything, "test-model").Run(func(args mock.Arguments) {
		messages := args[1].([]Message)
		capturedPrompt = messages[1].Content
	}).Return(expectedSummary, nil)

	result, err := summarizer.Summarize(context.Background(), request)
	assert.NoError(t, err)
	assert.True(t, result.Success)

	assert.Contains(t, capturedPrompt, "Create a brief summary of this conversation")
	assert.Contains(t, capturedPrompt, "IMPORTANT RULES:")
	assert.Contains(t, capturedPrompt, "Focus on: topics, user interests, key info, communication style")
	assert.Contains(t, capturedPrompt, "PREVIOUS CONTEXT: User previously asked about Python basics")
	assert.Contains(t, capturedPrompt, "NEW MESSAGES:")
	assert.Contains(t, capturedPrompt, "User123: Now I'm curious about Go!")
	assert.Contains(t, capturedPrompt, "GoExpert: Go excels at concurrency")
	assert.Contains(t, capturedPrompt, "CONVERSATION:")
	assert.Contains(t, capturedPrompt, "YOUR SUMMARY (direct output, no prefix):")

	mockAI.AssertExpectations(t)
}
