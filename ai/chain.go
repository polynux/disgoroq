package ai

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"polynux/disgoroq/logger"
)

// ProviderChain manages multiple providers in priority order for fallback support
type ProviderChain struct {
	providers []Provider
	validator *ResponseValidator
}

func selectedChatModel(provider Provider, fallback string) string {
	modelProvider, ok := provider.(modelAwareProvider)
	if !ok || modelProvider.ChatModelName() == "" {
		return fallback
	}

	return modelProvider.ChatModelName()
}

func selectedVisionModel(provider Provider, fallback string) string {
	modelProvider, ok := provider.(modelAwareProvider)
	if !ok || modelProvider.VisionModelName() == "" {
		return fallback
	}

	return modelProvider.VisionModelName()
}

// NewProviderChain creates a new provider chain with the given providers in priority order
func NewProviderChain(providers ...Provider) *ProviderChain {
	return &ProviderChain{
		providers: providers,
		validator: NewResponseValidator(WithMinLength(1)),
	}
}

// Name returns the name of the chain (concatenated provider names)
func (c *ProviderChain) Name() string {
	names := make([]string, len(c.providers))
	for i, provider := range c.providers {
		names[i] = provider.Name()
	}
	return strings.Join(names, "->")
}

// AvailableModels returns all available models from all providers
func (c *ProviderChain) AvailableModels() []ModelInfo {
	var allModels []ModelInfo
	seen := make(map[string]bool)

	for _, provider := range c.providers {
		models := provider.AvailableModels()
		for _, model := range models {
			// Avoid duplicates
			if !seen[model.Name] {
				allModels = append(allModels, model)
				seen[model.Name] = true
			}
		}
	}

	return allModels
}

// Chat attempts to generate a chat response using providers in priority order
func (c *ProviderChain) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var errors []error

	for i, provider := range c.providers {
		logger.Info("Trying AI provider",
			zap.String("provider", provider.Name()),
			zap.Int("attempt", i+1),
			zap.Int("total_providers", len(c.providers)),
			zap.String("model", selectedChatModel(provider, req.Model)))

		// Attempt to get response from this provider
		response, err := provider.Chat(ctx, req)

		if err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", provider.Name(), err))
			logger.Warn("AI provider failed",
				zap.String("provider", provider.Name()),
				zap.Error(err),
				zap.Int("attempt", i+1))

			// Continue to next provider if available
			if i < len(c.providers)-1 {
				continue
			}

			// This was the last provider, return aggregated error
			return nil, fmt.Errorf("all providers exhausted: %v", errors)
		}

		// Validate the response
		validation := c.validator.ValidateChatResponse(response)

		if validation.IsValid {
			// Success! Return the response with provider metadata
			if i > 0 {
				logger.Info("AI fallback succeeded",
					zap.String("provider", provider.Name()),
					zap.Int("fallback_attempt", i+1),
					zap.String("original_provider", c.providers[0].Name()))
			}

			if response.Provider == "" {
				response.Provider = provider.Name()
			}
			if response.Model == "" {
				response.Model = selectedChatModel(provider, req.Model)
			}

			return response, nil
		}

		// Empty/invalid response - log and try next provider
		errors = append(errors, fmt.Errorf("%s: empty response (%s)", provider.Name(), validation.Reason))
		logger.Warn("AI provider returned empty response",
			zap.String("provider", provider.Name()),
			zap.String("reason", validation.Reason),
			zap.Int("attempt", i+1))

		// Continue to next provider if available
		if i < len(c.providers)-1 {
			continue
		}

		// This was the last provider, return aggregated error
		return nil, fmt.Errorf("all providers exhausted with empty responses: %v", errors)
	}

	// Should not reach here, but handle the case
	return nil, fmt.Errorf("no providers available")
}

// Vision attempts to generate a vision response using providers in priority order
func (c *ProviderChain) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	var errors []error

	for i, provider := range c.providers {
		logger.Info("Trying AI provider for vision",
			zap.String("provider", provider.Name()),
			zap.Int("attempt", i+1),
			zap.Int("total_providers", len(c.providers)),
			zap.String("model", selectedVisionModel(provider, req.Model)))

		// Attempt to get response from this provider
		response, err := provider.Vision(ctx, req)

		if err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", provider.Name(), err))
			logger.Warn("AI provider vision failed",
				zap.String("provider", provider.Name()),
				zap.Error(err),
				zap.Int("attempt", i+1))

			// Continue to next provider if available
			if i < len(c.providers)-1 {
				continue
			}

			// This was the last provider, return aggregated error
			return nil, fmt.Errorf("all providers exhausted: %v", errors)
		}

		// Validate the response
		validation := c.validator.ValidateVisionResponse(response)

		if validation.IsValid {
			// Success! Return the response
			if i > 0 {
				logger.Info("AI fallback vision succeeded",
					zap.String("provider", provider.Name()),
					zap.Int("fallback_attempt", i+1),
					zap.String("original_provider", c.providers[0].Name()))
			}

			if response.Provider == "" {
				response.Provider = provider.Name()
			}
			if response.Model == "" {
				response.Model = selectedVisionModel(provider, req.Model)
			}

			return response, nil
		}

		// Empty response - log and try next provider
		errors = append(errors, fmt.Errorf("%s: empty response (%s)", provider.Name(), validation.Reason))
		logger.Warn("AI provider vision returned empty response",
			zap.String("provider", provider.Name()),
			zap.String("reason", validation.Reason),
			zap.Int("attempt", i+1))

		// Continue to next provider if available
		if i < len(c.providers)-1 {
			continue
		}

		// This was the last provider, return aggregated error
		return nil, fmt.Errorf("all providers exhausted with empty responses: %v", errors)
	}

	// Should not reach here, but handle the case
	return nil, fmt.Errorf("no providers available")
}

// GetProvider returns the provider at the given index
func (c *ProviderChain) GetProvider(index int) (Provider, error) {
	if index < 0 || index >= len(c.providers) {
		return nil, fmt.Errorf("provider index %d out of range (0-%d)", index, len(c.providers)-1)
	}
	return c.providers[index], nil
}

// ProviderCount returns the number of providers in the chain
func (c *ProviderChain) ProviderCount() int {
	return len(c.providers)
}

// IsFallbackAvailable returns true if there are fallback providers available
func (c *ProviderChain) IsFallbackAvailable() bool {
	return len(c.providers) > 1
}
