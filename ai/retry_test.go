package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"polynux/disgoroq/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockProvider is a mock implementation of the Provider interface for testing
type MockProvider struct {
	mock.Mock
	supportsInlineImages func(chatModel, visionModel string) bool
}

func (m *MockProvider) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockProvider) AvailableModels() []ModelInfo {
	args := m.Called()
	return args.Get(0).([]ModelInfo)
}

func (m *MockProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ChatResponse), args.Error(1)
}

func (m *MockProvider) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*VisionResponse), args.Error(1)
}

func (m *MockProvider) SupportsInlineImages(chatModel, visionModel string) bool {
	if m.supportsInlineImages == nil {
		return false
	}
	return m.supportsInlineImages(chatModel, visionModel)
}

func TestNewRetryWrapper(t *testing.T) {
	mockProvider := new(MockProvider)
	config := DefaultRetryConfig()

	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)

	assert.NotNil(t, wrapper)
	assert.Equal(t, mockProvider, wrapper.provider)
	assert.Equal(t, config, wrapper.config)
	assert.NotNil(t, wrapper.validator)
	assert.Equal(t, 1, wrapper.minResponseLength)
}

func TestRetryWrapperName(t *testing.T) {
	mockProvider := new(MockProvider)
	mockProvider.On("Name").Return("test-provider")

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	name := wrapper.Name()

	assert.Equal(t, "test-provider", name)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperAvailableModels(t *testing.T) {
	mockProvider := new(MockProvider)
	expectedModels := []ModelInfo{
		{Name: "model1", Provider: "test", Capabilities: []string{"chat"}},
		{Name: "model2", Provider: "test", Capabilities: []string{"vision"}},
	}
	mockProvider.On("AvailableModels").Return(expectedModels)

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	models := wrapper.AvailableModels()

	assert.Equal(t, expectedModels, models)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatSuccessFirstAttempt(t *testing.T) {
	mockProvider := new(MockProvider)
	expectedResponse := &ChatResponse{
		Content:      "Hello, world!",
		Model:        "test-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Content, response.Content)
	assert.Equal(t, expectedResponse.Model, response.Model)
	assert.Equal(t, "test-provider", response.Provider)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatAcceptsDiscordStyleUnicodeResponse(t *testing.T) {
	mockProvider := new(MockProvider)
	expectedResponse := &ChatResponse{
		Content:      ":criminel3: cancel squad en route 🏃‍♂️💨 darky & may sur la sellette, j'vais préparer les pitchforks et les hashtags 🔥",
		Model:        "test-model",
		TokensUsed:   42,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(expectedResponse, nil).Once()

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Content, response.Content)
	assert.Equal(t, "test-provider", response.Provider)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatAnnotatesModelWhenProviderLeavesItEmpty(t *testing.T) {
	mockProvider := new(MockProvider)
	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(&ChatResponse{
		Content:      "Hello, world!",
		Model:        "",
		TokensUsed:   10,
		FinishReason: "stop",
	}, nil)

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})

	assert.NoError(t, err)
	assert.Equal(t, "test-chat-model", response.Model)
	assert.Equal(t, "test-provider", response.Provider)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatFallsBackToImageDescriptions(t *testing.T) {
	mockProvider := new(MockProvider)
	expectedResponse := &ChatResponse{
		Content:      "described",
		Model:        "test-chat-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Vision", mock.Anything, mock.MatchedBy(func(req *VisionRequest) bool {
		return req.Model == "test-vision-model" &&
			req.ImageURL == "https://example.com/cat.png" &&
			req.Instruction == defaultVisionInstruction
	})).Return(&VisionResponse{
		Description: "a cat",
		Model:       "test-vision-model",
	}, nil).Once()
	mockProvider.On("Chat", mock.Anything, mock.MatchedBy(func(req *ChatRequest) bool {
		return req.Model == "test-chat-model" &&
			len(req.Images) == 0 &&
			len(req.Messages) == 1 &&
			len(req.Messages[0].ImageRefs) == 0 &&
			strings.Contains(req.Messages[0].Content, "<IMAGE_DESC>") &&
			strings.Contains(req.Messages[0].Content, "a cat")
	})).Return(expectedResponse, nil).Once()

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Messages: []Message{{
			Role:      "user",
			Content:   "look",
			ImageRefs: []int{0},
		}},
		Images: []ImageContext{{
			URL:  "https://example.com/cat.png",
			Type: "image/png",
		}},
	})

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatPreservesInlineImagesWhenProviderSupportsThem(t *testing.T) {
	mockProvider := new(MockProvider)
	mockProvider.supportsInlineImages = func(chatModel, visionModel string) bool {
		return chatModel == visionModel
	}

	expectedResponse := &ChatResponse{
		Content:      "inline",
		Model:        "shared-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.MatchedBy(func(req *ChatRequest) bool {
		return req.Model == "shared-model" &&
			len(req.Images) == 1 &&
			len(req.Messages) == 1 &&
			len(req.Messages[0].ImageRefs) == 1 &&
			!strings.Contains(req.Messages[0].Content, "<IMAGE_DESC>")
	})).Return(expectedResponse, nil).Once()

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "shared-model", "shared-model", nil)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Messages: []Message{{
			Role:      "user",
			Content:   "look",
			ImageRefs: []int{0},
		}},
		Images: []ImageContext{{
			URL:  "https://example.com/cat.png",
			Type: "image/png",
		}},
	})

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)
	mockProvider.AssertNotCalled(t, "Vision", mock.Anything, mock.Anything)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatEmptyResponseRetry(t *testing.T) {
	mockProvider := new(MockProvider)

	emptyResponse := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	validResponse := &ChatResponse{
		Content:      "Hello, world!",
		Model:        "test-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse, nil).Once()
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	config := DefaultRetryConfig()
	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	start := time.Now()
	response, err := wrapper.Chat(ctx, req)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, validResponse, response)
	assert.True(t, duration > 100*time.Millisecond, "Should have taken time due to retry delay")

	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatAPIErrorRetry(t *testing.T) {
	mockProvider := new(MockProvider)

	expectedError := errors.New("API error")

	validResponse := &ChatResponse{
		Content:      "Hello, world!",
		Model:        "test-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(nil, expectedError).Once()
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	config := DefaultRetryConfig()
	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, validResponse, response)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatMaxRetriesExhausted(t *testing.T) {
	mockProvider := new(MockProvider)

	emptyResponse := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse, nil).Times(3)

	config := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}

	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "empty response after 3 attempts")

	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatDisableRetryOnEmpty(t *testing.T) {
	mockProvider := new(MockProvider)

	emptyResponse := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse, nil).Once()

	config := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
		RetryOnEmpty:  false,
		RetryOnError:  true,
	}

	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "empty response after 1 attempts")

	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatDisableRetryOnError(t *testing.T) {
	mockProvider := new(MockProvider)

	expectedError := errors.New("API error")

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(nil, expectedError).Once()

	config := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  false,
	}

	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "failed after 1 attempts")

	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatZeroRetries(t *testing.T) {
	mockProvider := new(MockProvider)

	emptyResponse := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse, nil).Once()

	config := RetryConfig{
		MaxRetries:    0,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}

	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "empty response after 1 attempts")

	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperVisionSuccess(t *testing.T) {
	mockProvider := new(MockProvider)
	expectedResponse := &VisionResponse{
		Description:  "A beautiful sunset",
		Model:        "vision-model",
		TokensUsed:   20,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Vision", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &VisionRequest{
		Model:       "vision-model",
		Instruction: "Describe this image",
		ImageURL:    "https://example.com/image.jpg",
	}

	response, err := wrapper.Vision(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperVisionEmptyResponse(t *testing.T) {
	mockProvider := new(MockProvider)

	emptyResponse := &VisionResponse{
		Description:  "",
		Model:        "vision-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	validResponse := &VisionResponse{
		Description:  "A beautiful sunset",
		Model:        "vision-model",
		TokensUsed:   20,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Vision", mock.Anything, mock.Anything).Return(emptyResponse, nil).Once()
	mockProvider.On("Vision", mock.Anything, mock.Anything).Return(validResponse, nil).Once()

	config := DefaultRetryConfig()
	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &VisionRequest{
		Model:       "vision-model",
		Instruction: "Describe this image",
		ImageURL:    "https://example.com/image.jpg",
	}

	response, err := wrapper.Vision(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, validResponse, response)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperVisionMaxRetriesExhausted(t *testing.T) {
	mockProvider := new(MockProvider)

	emptyResponse := &VisionResponse{
		Description:  "",
		Model:        "vision-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Vision", mock.Anything, mock.Anything).Return(emptyResponse, nil).Times(3)

	config := RetryConfig{
		MaxRetries:    2,
		InitialDelay:  50 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
		RetryOnEmpty:  true,
		RetryOnError:  true,
	}

	wrapper := NewRetryWrapper(mockProvider, config, 1, "test-chat-model", "test-vision-model", nil)
	ctx := context.Background()
	req := &VisionRequest{
		Model:       "vision-model",
		Instruction: "Describe this image",
		ImageURL:    "https://example.com/image.jpg",
	}

	response, err := wrapper.Vision(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "empty response after 3 attempts")

	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatHonorsMinResponseLength(t *testing.T) {
	mockProvider := new(MockProvider)
	config := DefaultRetryConfig()
	config.MaxRetries = 0

	shortResponse := &ChatResponse{
		Content:      "hey",
		Model:        "test-model",
		TokensUsed:   3,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(shortResponse, nil)

	wrapper := NewRetryWrapper(mockProvider, config, 5, "test-chat-model", "test-vision-model", nil)
	_, err := wrapper.Chat(context.Background(), &ChatRequest{Model: "test-model"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatUsesAttachmentCacheHit(t *testing.T) {
	mockProvider := new(MockProvider)
	cache := newMemoryAttachmentCache()
	input := documentSummaryCacheInput(DocumentContext{
		URL:         "https://example.com/report.pdf",
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Size:        1234,
	}, 500)

	require.NoError(t, cache.PutAttachmentCache(context.Background(), database.AttachmentCacheEntry{
		AttachmentCacheKey: attachmentCacheKey(input, "test-provider", "test-chat-model"),
		Content:            "cached summary",
		SourceURL:          input.SourceURL,
		Filename:           input.Filename,
		ContentType:        input.ContentType,
		SizeBytes:          input.SizeBytes,
	}))

	mockProvider.On("Name").Return("test-provider")

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", cache)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Messages:        []Message{{Role: "user", Content: "ignored"}},
		AttachmentCache: cacheInputPtr(input),
	})

	assert.NoError(t, err)
	assert.Equal(t, "cached summary", response.Content)
	assert.Equal(t, "test-provider", response.Provider)
	assert.Equal(t, "test-chat-model", response.Model)
	mockProvider.AssertNotCalled(t, "Chat", mock.Anything, mock.Anything)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatStoresAttachmentCacheOnSuccess(t *testing.T) {
	mockProvider := new(MockProvider)
	cache := newMemoryAttachmentCache()
	input := documentSummaryCacheInput(DocumentContext{
		URL:         "https://example.com/report.pdf",
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Size:        1234,
	}, 500)

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(&ChatResponse{
		Content:      "fresh summary",
		Model:        "test-chat-model",
		FinishReason: "stop",
	}, nil).Once()

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", cache)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Messages:        []Message{{Role: "user", Content: "summarize"}},
		AttachmentCache: cacheInputPtr(input),
	})

	assert.NoError(t, err)
	assert.Equal(t, "fresh summary", response.Content)

	entry, found, err := cache.GetAttachmentCache(context.Background(), attachmentCacheKey(input, "test-provider", "test-chat-model"))
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "fresh summary", entry.Content)
	mockProvider.AssertExpectations(t)
}

func TestRetryWrapperChatUsesCachedImageDescriptions(t *testing.T) {
	mockProvider := new(MockProvider)
	cache := newMemoryAttachmentCache()
	image := ImageContext{
		URL:    "https://example.com/cat.png",
		Type:   "image/png",
		Width:  800,
		Height: 600,
		Size:   1234,
	}

	require.NoError(t, cache.PutAttachmentCache(context.Background(), database.AttachmentCacheEntry{
		AttachmentCacheKey: attachmentCacheKey(imageDescriptionCacheInput(image), "test-provider", "test-vision-model"),
		Content:            "a cached cat",
		SourceURL:          image.URL,
		ContentType:        image.Type,
		SizeBytes:          image.Size,
	}))

	mockProvider.On("Name").Return("test-provider")
	mockProvider.On("Chat", mock.Anything, mock.MatchedBy(func(req *ChatRequest) bool {
		return len(req.Images) == 0 &&
			len(req.Messages) == 1 &&
			strings.Contains(req.Messages[0].Content, "a cached cat") &&
			len(req.Messages[0].ImageRefs) == 0
	})).Return(&ChatResponse{
		Content: "described",
		Model:   "test-chat-model",
	}, nil).Once()

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig(), 1, "test-chat-model", "test-vision-model", cache)
	response, err := wrapper.Chat(context.Background(), &ChatRequest{
		Messages: []Message{{
			Role:      "user",
			Content:   "look",
			ImageRefs: []int{0},
		}},
		Images: []ImageContext{image},
	})

	assert.NoError(t, err)
	assert.Equal(t, "described", response.Content)
	mockProvider.AssertNotCalled(t, "Vision", mock.Anything, mock.Anything)
	mockProvider.AssertExpectations(t)
}
