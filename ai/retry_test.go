package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockProvider is a mock implementation of the Provider interface for testing
type MockProvider struct {
	mock.Mock
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

func TestNewRetryWrapper(t *testing.T) {
	mockProvider := new(MockProvider)
	config := DefaultRetryConfig()

	wrapper := NewRetryWrapper(mockProvider, config)

	assert.NotNil(t, wrapper)
	assert.Equal(t, mockProvider, wrapper.provider)
	assert.Equal(t, config, wrapper.config)
	assert.NotNil(t, wrapper.validator)
}

func TestRetryWrapperName(t *testing.T) {
	mockProvider := new(MockProvider)
	mockProvider.On("Name").Return("test-provider")

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig())
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

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig())
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

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig())
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := wrapper.Chat(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)
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
	wrapper := NewRetryWrapper(mockProvider, config)
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
	wrapper := NewRetryWrapper(mockProvider, config)
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

	wrapper := NewRetryWrapper(mockProvider, config)
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

	wrapper := NewRetryWrapper(mockProvider, config)
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

	wrapper := NewRetryWrapper(mockProvider, config)
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

	wrapper := NewRetryWrapper(mockProvider, config)
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

	wrapper := NewRetryWrapper(mockProvider, DefaultRetryConfig())
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
	wrapper := NewRetryWrapper(mockProvider, config)
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

	wrapper := NewRetryWrapper(mockProvider, config)
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
