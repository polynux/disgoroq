package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewProviderChain(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	chain := NewProviderChain(mockProvider1, mockProvider2)

	assert.NotNil(t, chain)
	assert.Len(t, chain.providers, 2)
	assert.Equal(t, mockProvider1, chain.providers[0])
	assert.Equal(t, mockProvider2, chain.providers[1])
	assert.NotNil(t, chain.validator)
}

func TestProviderChainName(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	mockProvider1.On("Name").Return("groq")
	mockProvider2.On("Name").Return("ollama")

	chain := NewProviderChain(mockProvider1, mockProvider2)
	name := chain.Name()

	assert.Equal(t, "groq->ollama", name)
	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainAvailableModels(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	models1 := []ModelInfo{
		{Name: "model1", Provider: "groq", Capabilities: []string{"chat"}},
		{Name: "model2", Provider: "groq", Capabilities: []string{"vision"}},
	}

	models2 := []ModelInfo{
		{Name: "model2", Provider: "ollama", Capabilities: []string{"vision"}}, // Duplicate
		{Name: "model3", Provider: "ollama", Capabilities: []string{"chat"}},
	}

	mockProvider1.On("AvailableModels").Return(models1)
	mockProvider2.On("AvailableModels").Return(models2)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	allModels := chain.AvailableModels()

	// Should have 3 unique models
	assert.Len(t, allModels, 3)

	// Check that model2 appears only once (from first provider)
	model2Count := 0
	for _, model := range allModels {
		if model.Name == "model2" {
			model2Count++
		}
	}
	assert.Equal(t, 1, model2Count)

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainChatSuccessFirstProvider(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	expectedResponse := &ChatResponse{
		Content:      "Hello, world!",
		Model:        "test-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider1.On("Name").Return("groq")
	mockProvider1.On("Chat", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := chain.Chat(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Content, response.Content)
	assert.Equal(t, expectedResponse.Model, response.Model)
	assert.Equal(t, "groq", response.Provider)

	// Second provider should not be called
	mockProvider2.AssertNotCalled(t, "Chat")
	mockProvider1.AssertExpectations(t)
}

func TestProviderChainChatKeepsFirstProviderForDiscordStyleUnicodeResponse(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	expectedResponse := &ChatResponse{
		Content:      ":criminel3: cancel squad en route 🏃‍♂️💨 darky & may sur la sellette, j'vais préparer les pitchforks et les hashtags 🔥",
		Model:        "test-model",
		TokensUsed:   10,
		FinishReason: "stop",
	}

	mockProvider1.On("Name").Return("groq")
	mockProvider1.On("Chat", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	response, err := chain.Chat(context.Background(), &ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Content, response.Content)
	assert.Equal(t, "groq", response.Provider)
	mockProvider2.AssertNotCalled(t, "Chat")
	mockProvider1.AssertExpectations(t)
}

func TestProviderChainChatFallbackOnError(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	// First provider fails
	expectedError := errors.New("API error")

	// Second provider succeeds
	expectedResponse := &ChatResponse{
		Content:      "Hello from fallback!",
		Model:        "test-model",
		TokensUsed:   15,
		FinishReason: "stop",
	}

	mockProvider1.On("Name").Return("groq")
	mockProvider1.On("Chat", mock.Anything, mock.Anything).Return(nil, expectedError)

	mockProvider2.On("Name").Return("ollama")
	mockProvider2.On("Chat", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := chain.Chat(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Content, response.Content)
	assert.Equal(t, expectedResponse.Model, response.Model)
	assert.Equal(t, "ollama", response.Provider)

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainChatFallbackOnEmptyResponse(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	// First provider returns empty response
	emptyResponse := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	// Second provider returns valid response
	validResponse := &ChatResponse{
		Content:      "Hello from fallback!",
		Model:        "test-model",
		TokensUsed:   15,
		FinishReason: "stop",
	}

	mockProvider1.On("Name").Return("groq")
	mockProvider1.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse, nil)

	mockProvider2.On("Name").Return("ollama")
	mockProvider2.On("Chat", mock.Anything, mock.Anything).Return(validResponse, nil)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := chain.Chat(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, validResponse.Content, response.Content)
	assert.Equal(t, validResponse.Model, response.Model)
	assert.Equal(t, "ollama", response.Provider)

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainChatAllProvidersFail(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	// Both providers fail
	expectedError1 := errors.New("API error 1")
	expectedError2 := errors.New("API error 2")

	mockProvider1.On("Name").Return("groq")
	mockProvider1.On("Chat", mock.Anything, mock.Anything).Return(nil, expectedError1)

	mockProvider2.On("Name").Return("ollama")
	mockProvider2.On("Chat", mock.Anything, mock.Anything).Return(nil, expectedError2)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := chain.Chat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "all providers exhausted")
	assert.Contains(t, err.Error(), "groq: API error 1")
	assert.Contains(t, err.Error(), "ollama: API error 2")

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainChatFillsProviderAndModelMetadata(t *testing.T) {
	mockProvider := new(MockProvider)
	mockProvider.On("Name").Return("groq")
	mockProvider.On("Chat", mock.Anything, mock.Anything).Return(&ChatResponse{
		Content:      "hello",
		Model:        "",
		TokensUsed:   7,
		FinishReason: "stop",
	}, nil)

	chain := NewProviderChain(mockProvider)
	response, err := chain.Chat(context.Background(), &ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})

	assert.NoError(t, err)
	assert.Equal(t, "groq", response.Provider)
	assert.Equal(t, "test-model", response.Model)
	mockProvider.AssertExpectations(t)
}

func TestProviderChainChatAllProvidersEmptyResponse(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	// Both providers return empty responses
	emptyResponse1 := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	emptyResponse2 := &ChatResponse{
		Content:      "",
		Model:        "test-model",
		TokensUsed:   0,
		FinishReason: "stop",
	}

	mockProvider1.On("Name").Return("groq")
	mockProvider1.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse1, nil)

	mockProvider2.On("Name").Return("ollama")
	mockProvider2.On("Chat", mock.Anything, mock.Anything).Return(emptyResponse2, nil)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	ctx := context.Background()
	req := &ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	response, err := chain.Chat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "all providers exhausted with empty responses")
	assert.Contains(t, err.Error(), "groq: empty response")
	assert.Contains(t, err.Error(), "ollama: empty response")

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainVisionSuccess(t *testing.T) {
	mockProvider := new(MockProvider)

	expectedResponse := &VisionResponse{
		Description:  "A beautiful sunset",
		Model:        "vision-model",
		TokensUsed:   20,
		FinishReason: "stop",
	}

	mockProvider.On("Name").Return("groq")
	mockProvider.On("Vision", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	chain := NewProviderChain(mockProvider)
	ctx := context.Background()
	req := &VisionRequest{
		Model:       "vision-model",
		Instruction: "Describe this image",
		ImageURL:    "https://example.com/image.jpg",
	}

	response, err := chain.Vision(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Description, response.Description)
	assert.Equal(t, expectedResponse.Model, response.Model)

	mockProvider.AssertExpectations(t)
}

func TestProviderChainVisionFallback(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	// First provider fails
	expectedError := errors.New("Vision not supported")

	// Second provider succeeds
	expectedResponse := &VisionResponse{
		Description:  "A beautiful sunset",
		Model:        "vision-model",
		TokensUsed:   20,
		FinishReason: "stop",
	}

	mockProvider1.On("Name").Return("ollama")
	mockProvider1.On("Vision", mock.Anything, mock.Anything).Return(nil, expectedError)

	mockProvider2.On("Name").Return("groq")
	mockProvider2.On("Vision", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	chain := NewProviderChain(mockProvider1, mockProvider2)
	ctx := context.Background()
	req := &VisionRequest{
		Model:       "vision-model",
		Instruction: "Describe this image",
		ImageURL:    "https://example.com/image.jpg",
	}

	response, err := chain.Vision(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse.Description, response.Description)
	assert.Equal(t, expectedResponse.Model, response.Model)

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestProviderChainGetProvider(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)

	chain := NewProviderChain(mockProvider1, mockProvider2)

	// Valid indices
	provider0, err0 := chain.GetProvider(0)
	assert.NoError(t, err0)
	assert.Equal(t, mockProvider1, provider0)

	provider1, err1 := chain.GetProvider(1)
	assert.NoError(t, err1)
	assert.Equal(t, mockProvider2, provider1)

	// Invalid indices
	providerNeg, errNeg := chain.GetProvider(-1)
	assert.Error(t, errNeg)
	assert.Nil(t, providerNeg)

	providerOut, errOut := chain.GetProvider(2)
	assert.Error(t, errOut)
	assert.Nil(t, providerOut)
}

func TestProviderChainProviderCount(t *testing.T) {
	mockProvider1 := new(MockProvider)
	mockProvider2 := new(MockProvider)
	mockProvider3 := new(MockProvider)

	chain1 := NewProviderChain(mockProvider1)
	assert.Equal(t, 1, chain1.ProviderCount())

	chain2 := NewProviderChain(mockProvider1, mockProvider2)
	assert.Equal(t, 2, chain2.ProviderCount())

	chain3 := NewProviderChain(mockProvider1, mockProvider2, mockProvider3)
	assert.Equal(t, 3, chain3.ProviderCount())
}

func TestProviderChainIsFallbackAvailable(t *testing.T) {
	mockProvider := new(MockProvider)

	// Single provider - no fallback
	chain1 := NewProviderChain(mockProvider)
	assert.False(t, chain1.IsFallbackAvailable())

	// Multiple providers - fallback available
	chain2 := NewProviderChain(mockProvider, mockProvider)
	assert.True(t, chain2.IsFallbackAvailable())

	// Three providers - fallback available
	chain3 := NewProviderChain(mockProvider, mockProvider, mockProvider)
	assert.True(t, chain3.IsFallbackAvailable())
}
