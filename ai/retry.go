package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	cache             database.AttachmentCache
	visionFallback    func(context.Context, *VisionRequest) (*VisionResponse, error)
}

type modelAwareProvider interface {
	ChatModelName() string
	VisionModelName() string
}

// NewRetryWrapper creates a new retry wrapper around a provider
func NewRetryWrapper(provider Provider, config RetryConfig, minResponseLength int, chatModel, visionModel string, cache database.AttachmentCache) *RetryWrapper {
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
		cache:             cache,
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

func (r *RetryWrapper) AttachmentCache() database.AttachmentCache {
	return r.cache
}

// SetVisionFallback registers an optional vision fallback used to describe
// images for chat when this provider's own vision model cannot.
func (r *RetryWrapper) SetVisionFallback(fallback func(context.Context, *VisionRequest) (*VisionResponse, error)) {
	r.visionFallback = fallback
}

func (r *RetryWrapper) ChatCacheScopes() []AttachmentCacheScope {
	if r.chatModel == "" {
		return nil
	}
	return []AttachmentCacheScope{{
		Provider: r.provider.Name(),
		Model:    r.chatModel,
	}}
}

// AvailableModels returns the available models from the wrapped provider
func (r *RetryWrapper) AvailableModels() []ModelInfo {
	return r.provider.AvailableModels()
}

// ProviderHTTPError reports a non-success HTTP status returned by a provider.
// Providers wrap their transport errors in this type so retry logic can tell a
// permanent account failure (quota, billing, auth) from a transient one.
type ProviderHTTPError struct {
	StatusCode int
	Message    string
}

func (e *ProviderHTTPError) Error() string {
	if e.StatusCode < http.StatusBadRequest {
		if e.Message != "" {
			return e.Message
		}
		return "provider request failed"
	}

	status := fmt.Sprintf("status %d", e.StatusCode)
	if text := strings.TrimSpace(http.StatusText(e.StatusCode)); text != "" {
		status = fmt.Sprintf("status %d %s", e.StatusCode, text)
	}
	if e.Message == "" {
		return "provider request failed: " + status
	}
	return "provider request failed: " + status + ": " + e.Message
}

// errorBodySnippet keeps a non-JSON error body (plain text, proxy HTML) readable
// in logs and visible to the quota check, without logging a whole page.
func errorBodySnippet(body []byte) string {
	const maxLen = 300
	text := []rune(strings.TrimSpace(string(body)))
	if len(text) > maxLen {
		return string(text[:maxLen]) + "…"
	}
	return string(text)
}

// quotaErrorHints are the wordings providers use when the account itself cannot
// serve the request until it is paid for. Keep them specific: words like
// "billing" or "subscribe" also show up in transient rate-limit bodies (Groq's
// 429 links to console.groq.com/settings/billing), which must still be retried.
var quotaErrorHints = []string{
	"usage credits", // Ollama Cloud: "Add usage credits or subscribe ..."
	"insufficient_quota",
	"payment required",
}

func isQuotaMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, hint := range quotaErrorHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// isRetryableProviderError reports whether err is worth another attempt. Quota,
// billing and auth failures are permanent: retrying them only burns calls.
func isRetryableProviderError(err error) bool {
	if err == nil {
		return true
	}
	// The caller gave up; another attempt cannot succeed.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var statusErr *ProviderHTTPError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
			return false
		}
		if statusErr.StatusCode >= http.StatusInternalServerError {
			return true
		}
		// Other 4xx responses reject the request itself (bad parameter, model
		// option, unreachable image), so sending it again cannot succeed. Only
		// timeouts and rate limits are worth another attempt, unless the rate
		// limit says the account is out of credits.
		switch statusErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return !isQuotaMessage(statusErr.Message)
		}
		// OpenCode Go wraps upstream failures in a 400 whose error type is
		// "server_error"; those can be transient, unlike "invalid_request_error".
		if statusErr.StatusCode == http.StatusBadRequest && strings.Contains(statusErr.Message, `"server_error"`) {
			return !isQuotaMessage(statusErr.Message)
		}
		if statusErr.StatusCode >= http.StatusBadRequest {
			return false
		}
		return !isQuotaMessage(statusErr.Message)
	}

	// Providers that do not expose a typed status still surface the quota
	// wording, e.g. "Add usage credits or subscribe".
	return !isQuotaMessage(err.Error())
}

// Chat performs a chat request with retry logic
func (r *RetryWrapper) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var lastErr error

	// Use provider-specific model
	requestWithModel := *req
	requestWithModel.Model = r.chatModel
	preparedRequest := r.prepareChatRequest(ctx, &requestWithModel)

	if cached := r.getCachedChatResponse(ctx, preparedRequest); cached != nil {
		return cached, nil
	}

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

			// Check if we should retry on error. Permanent failures (quota,
			// billing, auth) are not worth another call plus a backoff sleep.
			if !r.config.RetryOnError || !isRetryableProviderError(err) || attempt >= maxAttempts {
				return nil, fmt.Errorf("AI chat failed after %d attempts: %w", attempt, err)
			}

			// Wait before retry
			r.waitBeforeRetry(attempt)
			continue
		}

		// A completion model intentionally returns empty after its own single retry.
		if response.EmptyCompletion {
			if response.Model == "" {
				response.Model = preparedRequest.Model
			}
			response.Provider = r.provider.Name()
			return response, nil
		}

		// Validate the response
		validation := r.validator.ValidateChatResponse(response)

		if validation.IsValid {
			if response.Model == "" {
				response.Model = preparedRequest.Model
			}
			response.Provider = r.provider.Name()
			r.storeCachedChatResponse(ctx, preparedRequest, response)

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

func (r *RetryWrapper) getCachedChatResponse(ctx context.Context, req *ChatRequest) *ChatResponse {
	if r.cache == nil || req.AttachmentCache == nil {
		return nil
	}

	entry, found, err := r.cache.GetAttachmentCache(ctx, attachmentCacheKey(*req.AttachmentCache, r.provider.Name(), req.Model))
	if err != nil {
		logger.Warn("Failed to read attachment chat cache",
			zap.Error(err),
			zap.String("provider", r.provider.Name()),
			zap.String("model", req.Model),
			zap.String("cache_kind", req.AttachmentCache.Kind))
		return nil
	}
	if !found {
		return nil
	}

	content := strings.TrimSpace(entry.Content)
	if content == "" {
		return nil
	}

	logger.Debug("Using cached attachment chat response",
		zap.String("provider", r.provider.Name()),
		zap.String("model", req.Model),
		zap.String("cache_kind", req.AttachmentCache.Kind))

	return &ChatResponse{
		Content:      content,
		Provider:     r.provider.Name(),
		Model:        req.Model,
		FinishReason: "cache",
	}
}

func (r *RetryWrapper) storeCachedChatResponse(ctx context.Context, req *ChatRequest, response *ChatResponse) {
	if r.cache == nil || req.AttachmentCache == nil {
		return
	}

	content := strings.TrimSpace(response.Content)
	if content == "" {
		return
	}

	providerName := response.Provider
	if providerName == "" {
		providerName = r.provider.Name()
	}
	modelName := response.Model
	if modelName == "" {
		modelName = req.Model
	}

	if err := r.cache.PutAttachmentCache(ctx, database.AttachmentCacheEntry{
		AttachmentCacheKey: attachmentCacheKey(*req.AttachmentCache, providerName, modelName),
		Content:            content,
		SourceURL:          req.AttachmentCache.SourceURL,
		Filename:           req.AttachmentCache.Filename,
		ContentType:        req.AttachmentCache.ContentType,
		SizeBytes:          req.AttachmentCache.SizeBytes,
	}); err != nil {
		logger.Warn("Failed to write attachment chat cache",
			zap.Error(err),
			zap.String("provider", providerName),
			zap.String("model", modelName),
			zap.String("cache_kind", req.AttachmentCache.Kind))
	}
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

			if !r.config.RetryOnError || !isRetryableProviderError(err) || attempt >= maxAttempts {
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
