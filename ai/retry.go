package ai

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
)

// RetryWrapper wraps a Provider with retry logic including exponential backoff
type RetryWrapper struct {
	provider          Provider
	config            RetryConfig
	validator         *ResponseValidator
	minResponseLength int
	chatModel         string // Provider-specific chat model
	visionModel       string // Provider-specific vision model
}

type modelAwareProvider interface {
	ChatModelName() string
	VisionModelName() string
}

// NewRetryWrapper creates a new retry wrapper around a provider
func NewRetryWrapper(provider Provider, config RetryConfig, minResponseLength int, chatModel, visionModel string) *RetryWrapper {
	if minResponseLength < 1 {
		minResponseLength = 1
	}

	return &RetryWrapper{
		provider:          provider,
		config:            config,
		validator:         NewResponseValidator(WithMinLength(minResponseLength)),
		minResponseLength: minResponseLength,
		chatModel:         chatModel,
		visionModel:       visionModel,
	}
}

// Name returns the name of the wrapped provider
func (r *RetryWrapper) Name() string {
	return r.provider.Name()
}

func (r *RetryWrapper) ChatModelName() string {
	return r.chatModel
}

func (r *RetryWrapper) VisionModelName() string {
	return r.visionModel
}

// AvailableModels returns the available models from the wrapped provider
func (r *RetryWrapper) AvailableModels() []ModelInfo {
	return r.provider.AvailableModels()
}

// Chat performs a chat request with retry logic
func (r *RetryWrapper) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var lastErr error

	// Use provider-specific model
	requestWithModel := *req
	requestWithModel.Model = r.chatModel
	preparedRequest := r.prepareChatRequest(ctx, &requestWithModel)

	// Attempt up to MaxRetries + 1 times (initial attempt + retries)
	maxAttempts := r.config.MaxRetries + 1

	// In QuickFail mode, only attempt once (no retries)
	if r.config.QuickFail {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		logger.Debug("AI chat attempt",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", maxAttempts),
			zap.String("provider", r.provider.Name()),
			zap.String("model", preparedRequest.Model))

		// Make the API call
		start := time.Now()
		response, err := r.provider.Chat(ctx, preparedRequest)
		duration := time.Since(start)

		// Handle API error
		if err != nil {
			lastErr = err
			logger.Error("AI chat API error",
				zap.Error(err),
				zap.Int("attempt", attempt),
				zap.String("provider", r.provider.Name()),
				zap.Duration("duration", duration))

			// In QuickFail mode, return immediately on first error
			if r.config.QuickFail {
				return nil, fmt.Errorf("AI chat failed (quick-fail): %w", err)
			}

			// Check if we should retry on error
			if !r.config.RetryOnError || attempt >= maxAttempts {
				return nil, fmt.Errorf("AI chat failed after %d attempts: %w", attempt, err)
			}

			// Wait before retry
			r.waitBeforeRetry(attempt)
			continue
		}

		// Validate the response
		validation := r.validator.ValidateChatResponse(response)

		if validation.IsValid {
			if response.Model == "" {
				response.Model = preparedRequest.Model
			}
			response.Provider = r.provider.Name()

			// Success! Return the response
			if attempt > 1 {
				logger.Info("AI chat succeeded after retries",
					zap.Int("attempts", attempt),
					zap.String("provider", r.provider.Name()),
					zap.Duration("total_duration", time.Since(start)),
					zap.Int("response_length", len(response.Content)))
			}
			return response, nil
		}

		// Empty/invalid response detected
		lastErr = fmt.Errorf("empty response: %s", validation.Reason)

		logger.Warn("AI chat returned empty response",
			zap.String("reason", validation.Reason),
			zap.Int("attempt", attempt),
			zap.String("provider", r.provider.Name()),
			zap.String("model", preparedRequest.Model),
			zap.String("content", response.Content),
			zap.String("finish_reason", response.FinishReason),
			zap.Int("tokens_used", response.TokensUsed),
			zap.Duration("duration", duration))

		// Log the empty response event to database
		if logger.IsDBLoggingEnabled() {
			ctxWithFields := context.WithValue(ctx, "attempt", attempt)
			ctxWithFields = context.WithValue(ctxWithFields, "provider", r.provider.Name())
			ctxWithFields = context.WithValue(ctxWithFields, "duration_ms", int64(duration.Milliseconds()))

			event := &database.BotEvent{
				Timestamp: time.Now(),
				EventType: database.EventEmptyResponse,
				Details: &database.EventDetails{
					Model:   preparedRequest.Model,
					Context: validation.Reason,
				},
				DurationMS: duration.Milliseconds(),
			}
			logger.LogEvent(ctxWithFields, event)
		}

		// In QuickFail mode, return immediately on empty response
		if r.config.QuickFail {
			return nil, fmt.Errorf("AI chat returned empty response (quick-fail): %s", validation.Reason)
		}

		// Check if we should retry on empty response
		if !r.config.RetryOnEmpty || attempt >= maxAttempts {
			return nil, fmt.Errorf("AI chat returned empty response after %d attempts: %s", attempt, validation.Reason)
		}

		// Wait before retry
		r.waitBeforeRetry(attempt)
	}

	// All attempts exhausted
	if lastErr != nil {
		return nil, fmt.Errorf("AI chat exhausted all %d attempts: %w", maxAttempts, lastErr)
	}

	// This should not happen, but handle the case
	return nil, fmt.Errorf("AI chat failed after %d attempts with unknown error", maxAttempts)
}

// waitBeforeRetry waits for the appropriate delay before the next retry attempt
func (r *RetryWrapper) waitBeforeRetry(attempt int) {
	delay := r.config.GetDelayForAttempt(attempt)

	logger.Debug("Waiting before retry",
		zap.Int("attempt", attempt+1), // Next attempt
		zap.Duration("delay", delay),
		zap.String("provider", r.provider.Name()))

	time.Sleep(delay)
}

// Vision performs a vision request with retry logic
func (r *RetryWrapper) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	var lastErr error

	// Use provider-specific model
	requestWithModel := *req
	requestWithModel.Model = r.visionModel

	maxAttempts := r.config.MaxRetries + 1

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		logger.Debug("AI vision attempt",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", maxAttempts),
			zap.String("provider", r.provider.Name()),
			zap.String("model", requestWithModel.Model))

		response, err := r.provider.Vision(ctx, &requestWithModel)

		if err != nil {
			lastErr = err
			logger.Error("AI vision API error",
				zap.Error(err),
				zap.Int("attempt", attempt),
				zap.String("provider", r.provider.Name()))

			if !r.config.RetryOnError || attempt >= maxAttempts {
				return nil, fmt.Errorf("AI vision failed after %d attempts: %w", attempt, err)
			}

			r.waitBeforeRetry(attempt)
			continue
		}

		// Validate the vision response
		validation := r.validator.ValidateVisionResponse(response)

		if validation.IsValid {
			if response.Model == "" {
				response.Model = requestWithModel.Model
			}
			response.Provider = r.provider.Name()

			if attempt > 1 {
				logger.Info("AI vision succeeded after retries",
					zap.Int("attempts", attempt),
					zap.String("provider", r.provider.Name()))
			}
			return response, nil
		}

		// Empty response detected
		lastErr = fmt.Errorf("empty response: %s", validation.Reason)

		logger.Warn("AI vision returned empty response",
			zap.String("reason", validation.Reason),
			zap.Int("attempt", attempt),
			zap.String("provider", r.provider.Name()))

		if !r.config.RetryOnEmpty || attempt >= maxAttempts {
			return nil, fmt.Errorf("AI vision returned empty response after %d attempts: %s", attempt, validation.Reason)
		}

		r.waitBeforeRetry(attempt)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("AI vision exhausted all %d attempts: %w", maxAttempts, lastErr)
	}

	return nil, fmt.Errorf("AI vision failed after %d attempts with unknown error", maxAttempts)
}
