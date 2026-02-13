package ai

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"go.uber.org/zap"
	"polynux/disgoroq/logger"
)

// ServiceConfig contains configuration for the AI service
type ServiceConfig struct {
	// Primary provider configuration
	GroqAPIKey      string
	GroqModel       string
	GroqVisionModel string

	// Fallback provider configuration
	OllamaEnabled     bool
	OllamaURL         string
	OllamaModel       string
	OllamaVisionModel string

	// Retry configuration
	RetryConfig RetryConfig

	// Validation configuration
	MinResponseLength int

	// Fallback configuration
	FallbackEnabled bool
}

// LoadServiceConfig loads AI service configuration from environment variables
func LoadServiceConfig() ServiceConfig {
	config := ServiceConfig{
		GroqAPIKey:        os.Getenv("GROQ_API_KEY"),
		GroqModel:         getEnvWithDefault("GROQ_MODEL", "llama-3.3-70b-versatile"),
		GroqVisionModel:   getEnvWithDefault("GROQ_VISION_MODEL", "llama-3.2-11b-vision-preview"),
		OllamaEnabled:     parseBoolEnv(os.Getenv("OLLAMA_ENABLED"), false),
		OllamaURL:         getEnvWithDefault("OLLAMA_API_URL", "http://localhost:11434"),
		OllamaModel:       getEnvWithDefault("OLLAMA_MODEL", "dolphin3"),
		OllamaVisionModel: getEnvWithDefault("OLLAMA_VISION_MODEL", "llava:13b"),
		RetryConfig:       LoadRetryConfigFromEnv(),
		MinResponseLength: getIntEnvWithDefault("AI_MIN_RESPONSE_LENGTH", 1),
		FallbackEnabled:   parseBoolEnv(os.Getenv("AI_FALLBACK_ENABLED"), true),
	}

	return config
}

// getEnvWithDefault returns the environment variable value or default if not set
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getIntEnvWithDefault returns the environment variable as int or default if not set/invalid
func getIntEnvWithDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil && intVal >= 0 {
			return intVal
		}
	}
	return defaultValue
}

// Validate checks if the service configuration is valid
func (c *ServiceConfig) Validate() error {
	if c.GroqAPIKey == "" {
		return fmt.Errorf("GROQ_API_KEY is required")
	}

	if err := c.RetryConfig.Validate(); err != nil {
		return fmt.Errorf("invalid retry config: %w", err)
	}

	if c.MinResponseLength < 1 {
		return fmt.Errorf("minimum response length must be at least 1")
	}

	return nil
}

// Service is the high-level AI service that orchestrates providers, retries, and fallback
type Service struct {
	config   ServiceConfig
	provider Provider
	chain    *ProviderChain
}

// NewService creates a new AI service with the given configuration
func NewService(config ServiceConfig) *Service {
	var wrappedProviders []Provider

	// Groq as primary provider
	if config.GroqAPIKey != "" {
		groq := NewGroqProvider(config.GroqAPIKey)
		wrappedGroq := NewRetryWrapper(groq, config.RetryConfig, config.GroqModel, config.GroqVisionModel)
		wrappedProviders = append(wrappedProviders, wrappedGroq)
	}

	// Ollama as fallback if enabled
	if config.OllamaEnabled && config.FallbackEnabled {
		if ollama, err := NewOllamaProvider(); err == nil {
			wrappedOllama := NewRetryWrapper(ollama, config.RetryConfig, config.OllamaModel, config.OllamaVisionModel)
			wrappedProviders = append(wrappedProviders, wrappedOllama)
		} else {
			logger.Warn("Failed to create Ollama provider", zap.Error(err))
		}
	}

	if len(wrappedProviders) == 0 {
		logger.Error("No AI providers available - check configuration")
		return nil
	}

	// Create chain from wrapped providers
	chain := NewProviderChain(wrappedProviders...)

	return &Service{
		config:   config,
		provider: chain,
		chain:    chain,
	}
}

// Chat generates a chat response using the configured providers with retry and fallback
func (s *Service) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	start := time.Now()

	logger.Debug("Starting AI service chat",
		zap.String("model", req.Model),
		zap.Int("message_count", len(req.Messages)),
		zap.Bool("has_images", len(req.Images) > 0))

	// Make the API call through the provider chain with retry wrapper
	response, err := s.provider.Chat(ctx, req)

	duration := time.Since(start)

	if err != nil {
		logger.Error("AI service chat failed",
			zap.Error(err),
			zap.String("model", req.Model),
			zap.Duration("duration", duration))
		return nil, fmt.Errorf("AI service failed: %w", err)
	}

	// Log success
	logger.Info("AI service chat succeeded",
		zap.String("model", req.Model),
		zap.String("provider", s.provider.Name()),
		zap.Duration("duration", duration),
		zap.Int("response_length", len(response.Content)),
		zap.Int("tokens_used", response.TokensUsed))

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
	logger.Info("AI service vision succeeded",
		zap.String("model", req.Model),
		zap.String("provider", s.provider.Name()),
		zap.Duration("duration", duration),
		zap.Int("description_length", len(response.Description)),
		zap.Int("tokens_used", response.TokensUsed))

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

// GetProviderInfo returns information about the current provider configuration
func (s *Service) GetProviderInfo() map[string]interface{} {
	info := map[string]interface{}{
		"primary_provider": "groq",
		"fallback_enabled": s.config.FallbackEnabled,
		"ollama_enabled":   s.config.OllamaEnabled,
		"retry_config": map[string]interface{}{
			"max_retries":    s.config.RetryConfig.MaxRetries,
			"initial_delay":  s.config.RetryConfig.InitialDelay.String(),
			"max_delay":      s.config.RetryConfig.MaxDelay.String(),
			"backoff_factor": s.config.RetryConfig.BackoffFactor,
			"retry_on_empty": s.config.RetryConfig.RetryOnEmpty,
			"retry_on_error": s.config.RetryConfig.RetryOnError,
		},
		"min_response_length": s.config.MinResponseLength,
	}

	if s.config.OllamaEnabled {
		info["ollama_url"] = s.config.OllamaURL
		info["ollama_model"] = s.config.OllamaModel
	}

	return info
}

// IsFallbackAvailable returns true if fallback providers are configured
func (s *Service) IsFallbackAvailable() bool {
	if s.chain != nil {
		return s.chain.IsFallbackAvailable()
	}
	return false
}
