package ai

import (
	"context"
	"fmt"
	"strings"
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
	VisionProvider  string

	// Primary provider configuration
	GroqAPIKey          string
	GroqModel           string
	GroqVisionModel     string
	GroqThinkingEnabled bool

	// Additional provider configuration
	OllamaEnabled               bool
	OllamaURL                   string
	OllamaModel                 string
	OllamaVisionModel           string
	OllamaThinkingEnabled       bool
	OllamaCompletion            bool
	OllamaCompletionSpeaker     string
	OllamaCompletionPromptChars int
	OllamaCompletionBotLines    int
	OllamaCompletionMediaTags   string

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
	FallbackOrder   []string

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
		if strings.TrimSpace(c.GroqVisionModel) == "" {
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
		requireOllamaVision := c.VisionProvider == "" || c.VisionProvider == ProviderOllama
		if requireOllamaVision && strings.TrimSpace(c.OllamaVisionModel) == "" {
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
		if strings.TrimSpace(c.OpencodeVisionModel) == "" {
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
		if strings.TrimSpace(c.OpenrouterVisionModel) == "" {
			return fmt.Errorf("OpenRouter vision model is required when openrouter is the primary provider")
		}
	default:
		return fmt.Errorf("unsupported primary provider %q", c.PrimaryProvider)
	}

	switch c.VisionProvider {
	case "":
	case ProviderGroq:
		if c.GroqAPIKey == "" {
			return fmt.Errorf("GROQ_API_KEY is required when groq is the vision provider")
		}
		if strings.TrimSpace(c.GroqVisionModel) == "" {
			return fmt.Errorf("Groq vision model is required when groq is the vision provider")
		}
	case ProviderOllama:
		if !c.OllamaEnabled {
			return fmt.Errorf("ollama must be enabled when ollama is the vision provider")
		}
		if c.OllamaURL == "" {
			return fmt.Errorf("Ollama URL is required when ollama is the vision provider")
		}
		if strings.TrimSpace(c.OllamaVisionModel) == "" {
			return fmt.Errorf("Ollama vision model is required when ollama is the vision provider")
		}
	case ProviderOpencode:
		if !c.OpencodeEnabled {
			return fmt.Errorf("opencode must be enabled when opencode is the vision provider")
		}
		if c.OpencodeBaseURL == "" {
			return fmt.Errorf("OpenCode base URL is required when opencode is the vision provider")
		}
		if c.OpencodeAPIKey == "" {
			return fmt.Errorf("OpenCode API key is required when opencode is the vision provider")
		}
		if strings.TrimSpace(c.OpencodeVisionModel) == "" {
			return fmt.Errorf("OpenCode vision model is required when opencode is the vision provider")
		}
	case ProviderOpenrouter:
		if !c.OpenrouterEnabled {
			return fmt.Errorf("openrouter must be enabled when openrouter is the vision provider")
		}
		if c.OpenrouterBaseURL == "" {
			return fmt.Errorf("OpenRouter base URL is required when openrouter is the vision provider")
		}
		if c.OpenrouterAPIKey == "" {
			return fmt.Errorf("OpenRouter API key is required when openrouter is the vision provider")
		}
		if strings.TrimSpace(c.OpenrouterVisionModel) == "" {
			return fmt.Errorf("OpenRouter vision model is required when openrouter is the vision provider")
		}
	default:
		return fmt.Errorf("unsupported vision provider %q", c.VisionProvider)
	}

	seenFallback := make(map[string]bool, len(c.FallbackOrder))
	for _, provider := range c.FallbackOrder {
		if !isKnownProvider(provider) {
			return fmt.Errorf("unsupported fallback provider %q", provider)
		}
		if seenFallback[provider] {
			return fmt.Errorf("duplicate fallback provider %q", provider)
		}
		seenFallback[provider] = true
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

var defaultFallbackOrder = []string{
	ProviderGroq,
	ProviderOllama,
	ProviderOpencode,
	ProviderOpenrouter,
}

func isKnownProvider(providerName string) bool {
	switch providerName {
	case ProviderGroq, ProviderOllama, ProviderOpencode, ProviderOpenrouter:
		return true
	default:
		return false
	}
}

func (c ServiceConfig) isProviderEnabled(providerName string) bool {
	switch providerName {
	case ProviderGroq:
		return c.GroqAPIKey != ""
	case ProviderOllama:
		return c.OllamaEnabled
	case ProviderOpencode:
		return c.OpencodeEnabled && c.OpencodeAPIKey != ""
	case ProviderOpenrouter:
		return c.OpenrouterEnabled && c.OpenrouterAPIKey != ""
	default:
		return false
	}
}

func (c ServiceConfig) orderedProviderNames(primaryProvider string) []string {
	ordered := make([]string, 0, len(defaultFallbackOrder))
	seen := make(map[string]bool, len(defaultFallbackOrder))

	ordered = append(ordered, primaryProvider)
	seen[primaryProvider] = true

	for _, providerName := range c.FallbackOrder {
		if providerName == primaryProvider {
			logger.Info("Ignoring primary provider in ai.fallback_order",
				zap.String("provider", providerName))
			continue
		}
		if !isKnownProvider(providerName) {
			logger.Warn("Ignoring unknown provider in ai.fallback_order",
				zap.String("provider", providerName))
			continue
		}
		if seen[providerName] {
			continue
		}
		seen[providerName] = true
		if !c.isProviderEnabled(providerName) {
			logger.Info("Ignoring disabled provider in ai.fallback_order",
				zap.String("provider", providerName))
			continue
		}
		ordered = append(ordered, providerName)
	}

	for _, providerName := range defaultFallbackOrder {
		if seen[providerName] {
			continue
		}
		seen[providerName] = true
		ordered = append(ordered, providerName)
	}

	return ordered
}

func (c ServiceConfig) primaryProvider() string {
	if c.PrimaryProvider == "" {
		return ProviderGroq
	}
	return c.PrimaryProvider
}

// Service is the high-level AI service that orchestrates providers, retries, and fallback
type Service struct {
	config         ServiceConfig
	provider       Provider
	visionProvider Provider
	chain          *ProviderChain
	scopes         []AttachmentCacheScope
	tools          *ToolRegistry
}

// NewService creates a new AI service with the given configuration
func NewService(config ServiceConfig) *Service {
	// Build wrapped providers (with retry logic for each)
	var wrappedProviders []Provider
	var chatCacheScopes []AttachmentCacheScope
	primaryProvider := config.primaryProvider()
	builtProviders := make(map[string]*RetryWrapper)

	buildProvider := func(providerName string) *RetryWrapper {
		if existing, ok := builtProviders[providerName]; ok {
			return existing
		}
		var wrapper *RetryWrapper
		switch providerName {
		case ProviderGroq:
			if config.GroqAPIKey == "" {
				return nil
			}
			groqProvider := NewGroqProvider(config.GroqAPIKey, config.GroqThinkingEnabled)
			wrapper = NewRetryWrapper(
				groqProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.GroqModel,
				config.GroqVisionModel,
				config.AttachmentCache,
			)
		case ProviderOllama:
			if !config.OllamaEnabled {
				return nil
			}
			ollamaProvider, err := NewOllamaProvider(config.OllamaURL, config.OllamaThinkingEnabled)
			if err != nil {
				logger.Warn("Failed to create Ollama provider", zap.Error(err))
				return nil
			}
			ollamaProvider.SetCompletionMode(config.OllamaCompletion)
			ollamaProvider.SetCompletionConfig(config.OllamaCompletionSpeaker, config.OllamaCompletionPromptChars, config.OllamaCompletionBotLines, config.OllamaCompletionMediaTags)
			wrapper = NewRetryWrapper(
				ollamaProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.OllamaModel,
				config.OllamaVisionModel,
				config.AttachmentCache,
			)
		case ProviderOpencode:
			if !config.OpencodeEnabled || config.OpencodeAPIKey == "" {
				return nil
			}
			opencodeProvider, err := NewOpencodeProvider(config.OpencodeBaseURL, config.OpencodeAPIKey, config.OpencodeThinkingEnabled)
			if err != nil {
				logger.Warn("Failed to create OpenCode provider", zap.Error(err))
				return nil
			}
			wrapper = NewRetryWrapper(
				opencodeProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.OpencodeModel,
				config.OpencodeVisionModel,
				config.AttachmentCache,
			)
		case ProviderOpenrouter:
			if !config.OpenrouterEnabled || config.OpenrouterAPIKey == "" {
				return nil
			}
			openrouterProvider, err := NewOpenrouterProvider(config.OpenrouterBaseURL, config.OpenrouterAPIKey, config.OpenrouterThinkingEnabled)
			if err != nil {
				logger.Warn("Failed to create OpenRouter provider", zap.Error(err))
				return nil
			}
			wrapper = NewRetryWrapper(
				openrouterProvider,
				config.RetryConfig,
				config.MinResponseLength,
				config.OpenrouterModel,
				config.OpenrouterVisionModel,
				config.AttachmentCache,
			)
		}
		if wrapper != nil {
			builtProviders[providerName] = wrapper
		}
		return wrapper
	}

	addProvider := func(providerName string) {
		wrapper := buildProvider(providerName)
		if wrapper == nil {
			return
		}
		wrappedProviders = append(wrappedProviders, wrapper)
		chatCacheScopes = append(chatCacheScopes, AttachmentCacheScope{
			Provider: wrapper.Name(),
			Model:    wrapper.ChatModelName(),
		})
	}

	// Resolve the fallback order only when fallback is on, so a list left in the
	// config while fallback is off does not log misleading "ignoring" lines.
	orderedProviders := []string{primaryProvider}
	if config.FallbackEnabled {
		orderedProviders = config.orderedProviderNames(primaryProvider)
	}

	addProvider(primaryProvider)

	if config.FallbackEnabled {
		for _, providerName := range orderedProviders[1:] {
			addProvider(providerName)
		}
	}

	if len(wrappedProviders) == 0 {
		logger.Error("No AI providers available - check configuration")
		return nil
	}

	var visionProvider Provider
	var visionOrder []string
	if config.VisionProvider != "" {
		visionProviders := make([]Provider, 0, len(wrappedProviders)+1)
		seen := make(map[string]bool, len(wrappedProviders)+1)
		addVisionProvider := func(wrapper *RetryWrapper) {
			if wrapper == nil || seen[wrapper.Name()] || strings.TrimSpace(wrapper.VisionModelName()) == "" {
				return
			}
			seen[wrapper.Name()] = true
			visionProviders = append(visionProviders, wrapper)
			visionOrder = append(visionOrder, wrapper.Name())
		}

		configuredVision := buildProvider(config.VisionProvider)
		if configuredVision == nil {
			logger.Warn("Configured vision provider could not be created; image descriptions fall back to the other providers",
				zap.String("vision_provider", config.VisionProvider))
		}
		addVisionProvider(configuredVision)
		if config.FallbackEnabled {
			for _, providerName := range orderedProviders {
				addVisionProvider(builtProviders[providerName])
			}
		}

		if len(visionProviders) > 0 {
			visionChain := NewProviderChain(visionProviders...)
			visionProvider = visionChain
			for _, provider := range wrappedProviders {
				if wrapper, ok := provider.(*RetryWrapper); ok {
					wrapper.SetVisionChain(visionChain)
				}
			}
		}
	} else {
		// Describing an image for chat must survive one provider's vision model
		// failing, so each wrapper falls back to the other providers' vision models.
		for _, provider := range wrappedProviders {
			wrapper, ok := provider.(*RetryWrapper)
			if !ok {
				continue
			}
			others := make([]Provider, 0, len(wrappedProviders)-1)
			for _, other := range wrappedProviders {
				if other != provider {
					others = append(others, other)
				}
			}
			if len(others) > 0 {
				wrapper.SetVisionFallback(NewProviderChain(others...).Vision)
			}
		}
	}

	// Log the effective order once, so operators don't have to infer it from
	// per-request "Trying AI provider" lines.
	chatOrder := make([]string, 0, len(wrappedProviders))
	for _, provider := range wrappedProviders {
		chatOrder = append(chatOrder, provider.Name())
	}
	if config.VisionProvider != "" {
		logger.Info("AI provider order", zap.Strings("chat", chatOrder), zap.Strings("vision", visionOrder))
	} else {
		logger.Info("AI provider order", zap.Strings("chat", chatOrder), zap.String("vision", "each chat provider, then the others"))
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
		config:         config,
		provider:       finalProvider,
		visionProvider: visionProvider,
		chain:          chain,
		scopes:         chatCacheScopes,
		tools:          NewToolRegistry(config.ToolConfig),
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

	visionProvider := s.visionProvider
	if visionProvider == nil {
		visionProvider = s.provider
	}

	// Make the API call through the provider chain with retry wrapper
	response, err := visionProvider.Vision(ctx, req)

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
		providerName = visionProvider.Name()
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

// IsCompletionMode reports whether the response came from Ollama raw completion.
func (s *Service) IsCompletionMode(response *ChatResponse) bool {
	if !s.config.OllamaCompletion || response == nil {
		return false
	}
	provider := response.Provider
	if provider == "" {
		provider = s.Name()
	}
	return provider == ProviderOllama
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
		"vision_provider":    s.config.VisionProvider,
		"fallback_enabled":   s.config.FallbackEnabled,
		"fallback_order":     append([]string(nil), s.config.FallbackOrder...),
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
