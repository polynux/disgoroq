package ai

import (
	"fmt"
)

// SummaryProviderConfig describes how to build a standalone provider for
// memory summarization. It bypasses the ProviderChain so the requested
// model (memory.summary_model) is honored instead of being overwritten
// by the provider's own chat model.
type SummaryProviderConfig struct {
	Provider        string // groq, ollama, openrouter, opencode
	Model           string
	OllamaURL       string // Used when Provider is ollama
	APIKey          string // Used when Provider is groq/openrouter/opencode
	OpencodeBaseURL string // Used when Provider is opencode
}

// NewSummaryProvider builds a single standalone provider wrapped with retry
// logic for memory summarization. The returned Provider honors req.Model
// as passed by the caller (RetryWrapper sets it from config.Model).
func NewSummaryProvider(config SummaryProviderConfig) (Provider, error) {
	if config.Model == "" {
		return nil, fmt.Errorf("summary model is required")
	}

	var provider Provider
	switch config.Provider {
	case "ollama":
		p, err := NewOllamaProvider(config.OllamaURL, false)
		if err != nil {
			return nil, fmt.Errorf("failed to create ollama summary provider: %w", err)
		}
		provider = p
	case "groq":
		if config.APIKey == "" {
			return nil, fmt.Errorf("api key is required for groq summary provider")
		}
		provider = NewGroqProvider(config.APIKey, false)
	case "openrouter":
		if config.APIKey == "" {
			return nil, fmt.Errorf("api key is required for openrouter summary provider")
		}
		baseURL := config.OpencodeBaseURL
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
		p, err := NewOpenrouterProvider(baseURL, config.APIKey, false)
		if err != nil {
			return nil, fmt.Errorf("failed to create openrouter summary provider: %w", err)
		}
		provider = p
	case "opencode":
		p, err := NewOpencodeProvider(config.OpencodeBaseURL, config.APIKey, false)
		if err != nil {
			return nil, fmt.Errorf("failed to create opencode summary provider: %w", err)
		}
		provider = p
	case "":
		return nil, fmt.Errorf("summary provider is required")
	default:
		return nil, fmt.Errorf("unsupported summary provider %q", config.Provider)
	}

	return NewRetryWrapper(provider, DefaultRetryConfig(), 1, config.Model, "", nil), nil
}
