package ai

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
)

const (
	ProviderGroq       = "groq"
	ProviderOllama     = "ollama"
	ProviderOpencode   = "opencode"
	ProviderOpenrouter = "openrouter"
)

// ServiceConfig contains configuration for the AI service
type ServiceConfig struct {
	PrimaryProvider string

	// Primary provider configuration
	GroqAPIKey          string
	GroqModel           string
	GroqVisionModel     string
	GroqThinkingEnabled bool

	// Additional provider configuration
	OllamaEnabled         bool
	OllamaURL             string
	OllamaModel           string
	OllamaVisionModel     string
	OllamaThinkingEnabled bool

	OpencodeEnabled         bool
	OpencodeBaseURL         string
	OpencodeAPIKey          string
	OpencodeModel           string
	OpencodeVisionModel     string
	OpencodeThinkingEnabled bool

	OpenrouterEnabled         bool
	OpenrouterBaseURL         string
	OpenrouterAPIKey          string
	OpenrouterModel           string
	OpenrouterVisionModel     string
	OpenrouterThinkingEnabled bool

	// Retry configuration
	RetryConfig RetryConfig

	// Validation configuration
	MinResponseLength int

	// Fallback configuration
	FallbackEnabled bool

	// Optional persistent cache for detached attachment-to-text conversions.
	AttachmentCache database.AttachmentCache

	// Optional tool-calling runtime configuration.
	ToolConfig ToolRuntimeConfig
}

// Validate checks if the service configuration is valid
func (c *ServiceConfig) Validate() error {
	switch c.primaryProvider() {
	case ProviderGroq:
		if c.GroqAPIKey == "" {
			return fmt.Errorf("GROQ_API_KEY is required when groq is the primary provider")
		}
		if c.GroqModel == "" {
			return fmt.Errorf("Groq model is required when groq is the primary provider")
		}
		if c.GroqVisionModel == "" {
			return fmt.Errorf("Groq vision model is required when groq is the primary provider")
		}
	case ProviderOllama:
		if !c.OllamaEnabled {
			return fmt.Errorf("ollama must be enabled when ollama is the primary provider")
		}
		if c.OllamaURL == "" {
			return fmt.Errorf("Ollama URL is required when ollama is the primary provider")
		}
		if c.OllamaModel == "" {
			return fmt.Errorf("Ollama model is required when ollama is the primary provider")
		}
		if c.OllamaVisionModel == "" {
			return fmt.Errorf("Ollama vision model is required when ollama is the primary provider")
		}
	case ProviderOpencode:
		if !c.OpencodeEnabled {
			return fmt.Errorf("opencode must be enabled when opencode is the primary provider")
		}
		if c.OpencodeBaseURL == "" {
			return fmt.Errorf("OpenCode base URL is required when opencode is the primary provider")
		}
		if c.OpencodeAPIKey == "" {
			return fmt.Errorf("OpenCode API key is required when opencode is the primary provider")
		}
		if c.OpencodeModel == "" {
			return fmt.Errorf("OpenCode model is required when opencode is the primary provider")
		}
		if c.OpencodeVisionModel == "" {
			return fmt.Errorf("OpenCode vision model is required when opencode is the primary provider")
		}
	case ProviderOpenrouter:
		if !c.OpenrouterEnabled {
			return fmt.Errorf("openrouter must be enabled when openrouter is the primary provider")
		}
		if c.OpenrouterBaseURL == "" {
			return fmt.Errorf("OpenRouter base URL is required when openrouter is the primary provider")
		}
		if c.OpenrouterAPIKey == "" {
			return fmt.Errorf("OpenRouter API key is required when openrouter is the primary provider")
		}
		if c.OpenrouterModel == "" {
			return fmt.Errorf("OpenRouter model is required when openrouter is the primary provider")
		}
		if c.OpenrouterVisionModel == "" {
			return fmt.Errorf("OpenRouter vision model is required when openrouter is the primary provider")
		}
	default:
		return fmt.Errorf("unsupported primary provider %q", c.PrimaryProvider)
	}

	if err := c.RetryConfig.Validate(); err != nil {
		return fmt.Errorf("invalid retry config: %w", err)
	}

	if c.MinResponseLength < 1 {
		return fmt.Errorf("minimum response length must be at least 1")
	}

	if err := c.ToolConfig.Validate(); err != nil {
		return fmt.Errorf("invalid tool config: %w", err)
	}

	return nil
}

func (c ServiceConfig) primaryProvider() string {
	if c.PrimaryProvider == "" {
		return ProviderGroq
	}
	return c.PrimaryProvider
}

// Service is the high-level AI service that orchestrates providers, retries, and fallback
type Service struct {
	config   ServiceConfig
	provider Provider
	chain    *ProviderChain
	scopes   []AttachmentCacheScope
	tools    *ToolRegistry
}

// NewService creates a new AI service with the given configuration
func NewService(config ServiceConfig) *Service {
	// Build wrapped providers (with retry logic for each)
	var wrappedProviders []Provider
	var chatCacheScopes []AttachmentCacheScope
	primaryProvider := config.primaryProvider()

	addProvider := func(providerName string) {
		switch providerName {
		case ProviderGroq:
			if config.GroqAPIKey == "" {
				return
			}
			groqProvider := NewGroqProvider(config.GroqAPIKey, config.GroqThinkingEnabled)
			wrappedProviders = append(wrappedProviders, NewRetryWrapper(
				groqProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.GroqModel,
				config.GroqVisionModel,
				config.AttachmentCache,
			))
			chatCacheScopes = append(chatCacheScopes, AttachmentCacheScope{Provider: ProviderGroq, Model: config.GroqModel})
		case ProviderOllama:
			if !config.OllamaEnabled {
				return
			}
			ollamaProvider, err := NewOllamaProvider(config.OllamaURL, config.OllamaThinkingEnabled)
			if err != nil {
				logger.Warn("Failed to create Ollama provider", zap.Error(err))
				return
			}
			wrappedProviders = append(wrappedProviders, NewRetryWrapper(
				ollamaProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.OllamaModel,
				config.OllamaVisionModel,
				config.AttachmentCache,
			))
			chatCacheScopes = append(chatCacheScopes, AttachmentCacheScope{Provider: ProviderOllama, Model: config.OllamaModel})
		case ProviderOpencode:
			if !config.OpencodeEnabled || config.OpencodeAPIKey == "" {
				return
			}
			opencodeProvider, err := NewOpencodeProvider(config.OpencodeBaseURL, config.OpencodeAPIKey, config.OpencodeThinkingEnabled)
			if err != nil {
				logger.Warn("Failed to create OpenCode provider", zap.Error(err))
				return
			}
			wrappedProviders = append(wrappedProviders, NewRetryWrapper(
				opencodeProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.OpencodeModel,
				config.OpencodeVisionModel,
				config.AttachmentCache,
			))
			chatCacheScopes = append(chatCacheScopes, AttachmentCacheScope{Provider: ProviderOpencode, Model: config.OpencodeModel})
		case ProviderOpenrouter:
			if !config.OpenrouterEnabled || config.OpenrouterAPIKey == "" {
				return
			}
			openrouterProvider, err := NewOpenrouterProvider(config.OpenrouterBaseURL, config.OpenrouterAPIKey, config.OpenrouterThinkingEnabled)
			if err != nil {
				logger.Warn("Failed to create OpenRouter provider", zap.Error(err))
				return
			}
			wrappedProviders = append(wrappedProviders, NewRetryWrapper(
				openrouterProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.OpenrouterModel,
				config.OpenrouterVisionModel,
				config.AttachmentCache,
			))
			chatCacheScopes = append(chatCacheScopes, AttachmentCacheScope{Provider: ProviderOpenrouter, Model: config.OpenrouterModel})
		}
	}

	addProvider(primaryProvider)

	if config.FallbackEnabled {
		for _, providerName := range []string{ProviderGroq, ProviderOllama, ProviderOpencode, ProviderOpenrouter} {
			if providerName == primaryProvider {
				continue
			}
			addProvider(providerName)
		}
	}

	if len(wrappedProviders) == 0 {
		logger.Error("No AI providers available - check configuration")
		return nil
	}

	// Create provider chain if multiple providers
	var chain *ProviderChain
	var finalProvider Provider
	if len(wrappedProviders) > 1 {
		chain = NewProviderChain(wrappedProviders...)
		finalProvider = chain
	} else {
		finalProvider = wrappedProviders[0]
	}

	return &Service{
		config:   config,
		provider: finalProvider,
		chain:    chain,
		scopes:   chatCacheScopes,
		tools:    NewToolRegistry(config.ToolConfig),
	}
}

// Chat generates a chat response using the configured providers with retry and fallback
func (s *Service) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	start := time.Now()

	logger.Debug("Starting AI service chat",
		zap.String("model", req.Model),
		zap.Int("message_count", len(req.Messages)),
		zap.Bool("has_images", len(req.Images) > 0),
		zap.Bool("tools_enabled", s.tools != nil && s.tools.Enabled()))

	// Make the API call through the provider chain with retry wrapper
	response, err := s.chatWithTools(ctx, req)

	duration := time.Since(start)

	if err != nil {
		logger.Error("AI service chat failed",
			zap.Error(err),
			zap.String("model", req.Model),
			zap.Duration("duration", duration))
		return nil, fmt.Errorf("AI service failed: %w", err)
	}

	// Log success
	modelName := response.Model
	if modelName == "" {
		modelName = req.Model
	}
	providerName := response.Provider
	if providerName == "" {
		providerName = s.provider.Name()
	}
	logger.Info("AI service chat succeeded",
		zap.String("model", modelName),
		zap.String("provider", providerName),
		zap.Duration("duration", duration),
		zap.Int("response_length", len(response.Content)),
		zap.Int("tokens_used", response.TokensUsed),
		zap.Int("prompt_tokens", response.PromptTokens),
		zap.Int("completion_tokens", response.CompletionTokens),
		zap.Int("cached_tokens", response.CachedTokens),
		zap.Int("cache_write_tokens", response.CacheWriteTokens))

	return response, nil
}

// Vision generates a vision response using the configured providers with retry and fallback
func (s *Service) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	start := time.Now()

	logger.Debug("Starting AI service vision",
		zap.String("model", req.Model),
		zap.String("image_url", req.ImageURL))

	// Make the API call through the provider chain with retry wrapper
	response, err := s.provider.Vision(ctx, req)

	duration := time.Since(start)

	if err != nil {
		logger.Error("AI service vision failed",
			zap.Error(err),
			zap.String("model", req.Model),
			zap.Duration("duration", duration))
		return nil, fmt.Errorf("AI service vision failed: %w", err)
	}

	// Log success
	modelName := response.Model
	if modelName == "" {
		modelName = req.Model
	}
	providerName := response.Provider
	if providerName == "" {
		providerName = s.provider.Name()
	}
	logger.Info("AI service vision succeeded",
		zap.String("model", modelName),
		zap.String("provider", providerName),
		zap.Duration("duration", duration),
		zap.Int("description_length", len(response.Description)),
		zap.Int("tokens_used", response.TokensUsed),
		zap.Int("prompt_tokens", response.PromptTokens),
		zap.Int("completion_tokens", response.CompletionTokens),
		zap.Int("cached_tokens", response.CachedTokens),
		zap.Int("cache_write_tokens", response.CacheWriteTokens))

	return response, nil
}

// Name returns the name of the underlying provider
func (s *Service) Name() string {
	return s.provider.Name()
}

// AvailableModels returns available models from the underlying provider
func (s *Service) AvailableModels() []ModelInfo {
	return s.provider.AvailableModels()
}

func (s *Service) AttachmentCache() database.AttachmentCache {
	return s.config.AttachmentCache
}

func (s *Service) ChatCacheScopes() []AttachmentCacheScope {
	return append([]AttachmentCacheScope(nil), s.scopes...)
}

func (s *Service) ToolRegistry() *ToolRegistry {
	return s.tools
}

// GetProviderInfo returns information about the current provider configuration
func (s *Service) GetProviderInfo() map[string]interface{} {
	info := map[string]interface{}{
		"primary_provider":   s.config.primaryProvider(),
		"fallback_enabled":   s.config.FallbackEnabled,
		"groq_enabled":       s.config.GroqAPIKey != "",
		"ollama_enabled":     s.config.OllamaEnabled,
		"opencode_enabled":   s.config.OpencodeEnabled,
		"openrouter_enabled": s.config.OpenrouterEnabled,
		"retry_config": map[string]interface{}{
			"max_retries":    s.config.RetryConfig.MaxRetries,
			"initial_delay":  s.config.RetryConfig.InitialDelay.String(),
			"max_delay":      s.config.RetryConfig.MaxDelay.String(),
			"backoff_factor": s.config.RetryConfig.BackoffFactor,
			"retry_on_empty": s.config.RetryConfig.RetryOnEmpty,
			"retry_on_error": s.config.RetryConfig.RetryOnError,
		},
		"min_response_length": s.config.MinResponseLength,
		"tools_enabled":       s.config.ToolConfig.Enabled,
	}

	if s.config.OllamaEnabled {
		info["ollama_url"] = s.config.OllamaURL
		info["ollama_model"] = s.config.OllamaModel
	}
	if s.config.OpencodeEnabled {
		info["opencode_base_url"] = s.config.OpencodeBaseURL
		info["opencode_model"] = s.config.OpencodeModel
	}
	if s.config.OpenrouterEnabled {
		info["openrouter_base_url"] = s.config.OpenrouterBaseURL
		info["openrouter_model"] = s.config.OpenrouterModel
	}

	return info
}

// IsFallbackAvailable returns true if fallback providers are configured
func (s *Service) IsFallbackAvailable() bool {
	// Use stored chain reference instead of type assertion
	if s.chain != nil {
		return s.chain.IsFallbackAvailable()
	}
	return false
}
