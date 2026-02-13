package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaEmbeddingProvider implements the EmbeddingProvider interface using Ollama HTTP API
type OllamaEmbeddingProvider struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// OllamaEmbedRequest represents the request structure for Ollama embedding API
type OllamaEmbedRequest struct {
	Model    string `json:"model"`
	Input    string `json:"input"`
	Truncate bool   `json:"truncate"`
}

// OllamaEmbedResponse represents the response structure for Ollama embedding API
type OllamaEmbedResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float32 `json:"embeddings"`
	TotalDuration   int64       `json:"total_duration"`
	LoadDuration    int64       `json:"load_duration"`
	PromptEvalCount int         `json:"prompt_eval_count"`
}

// OllamaErrorResponse represents error responses from Ollama API
type OllamaErrorResponse struct {
	Error string `json:"error"`
}

// NewOllamaEmbeddingProvider creates a new Ollama embedding provider
func NewOllamaEmbeddingProvider() (*OllamaEmbeddingProvider, error) {
	return NewOllamaEmbeddingProviderWithConfig("http://localhost:11434", "nomic-embed-text")
}

// NewOllamaEmbeddingProviderWithConfig creates a new Ollama embedding provider with custom configuration
func NewOllamaEmbeddingProviderWithConfig(baseURL, model string) (*OllamaEmbeddingProvider, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}

	return &OllamaEmbeddingProvider{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// GenerateEmbedding generates an embedding for the given text using Ollama
func (p *OllamaEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	request := OllamaEmbedRequest{
		Model:    p.model,
		Input:    text,
		Truncate: true, // Auto-truncate to model's max context
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/embed", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to Ollama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errorResp OllamaErrorResponse
		if err := json.Unmarshal(body, &errorResp); err != nil {
			return nil, fmt.Errorf("Ollama API error (status %d): %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("Ollama API error: %s", errorResp.Error)
	}

	var embedResp OllamaEmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(embedResp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned from Ollama")
	}

	if len(embedResp.Embeddings[0]) != 768 {
		return nil, fmt.Errorf("unexpected embedding dimension: expected 768, got %d", len(embedResp.Embeddings[0]))
	}

	return embedResp.Embeddings[0], nil
}

// HealthCheck checks if Ollama is running and the model is available
func (p *OllamaEmbeddingProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Ollama health check failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Check if the model is available
	var tagsResponse struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read tags response: %w", err)
	}

	if err := json.Unmarshal(body, &tagsResponse); err != nil {
		return fmt.Errorf("failed to unmarshal tags response: %w", err)
	}

	modelAvailable := false
	for _, model := range tagsResponse.Models {
		// Check if model name matches (with or without tag suffix like ":latest")
		if model.Name == p.model || len(model.Name) > len(p.model) && model.Name[:len(p.model)] == p.model && model.Name[len(p.model)] == ':' {
			modelAvailable = true
			break
		}
	}

	if !modelAvailable {
		return fmt.Errorf("model '%s' not found in Ollama. Run: ollama pull %s", p.model, p.model)
	}

	return nil
}

// GetModel returns the model name being used
func (p *OllamaEmbeddingProvider) GetModel() string {
	return p.model
}

// GetBaseURL returns the base URL of the Ollama instance
func (p *OllamaEmbeddingProvider) GetBaseURL() string {
	return p.baseURL
}

// SetTimeout sets the HTTP client timeout
func (p *OllamaEmbeddingProvider) SetTimeout(timeout time.Duration) {
	p.httpClient.Timeout = timeout
}

// GenerateEmbeddingBatch generates embeddings for multiple texts in a single request
func (p *OllamaEmbeddingProvider) GenerateEmbeddingBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	// Ollama doesn't support batch embedding in a single request, so we do it sequentially
	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, err := p.GenerateEmbedding(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("failed to generate embedding for text %d: %w", i, err)
		}
		embeddings[i] = embedding
	}

	return embeddings, nil
}

// EstimateTokens provides a rough estimate of token count for the given text
func EstimateTokens(text string) int {
	// Rough estimation: ~4 characters per token for English text
	return len(text) / 4
}

// ValidateTextLength checks if the text is within reasonable limits for the model
func (p *OllamaEmbeddingProvider) ValidateTextLength(text string) error {
	// nomic-embed-text supports up to 8192 tokens
	maxTokens := 8192
	estimatedTokens := EstimateTokens(text)

	if estimatedTokens > maxTokens {
		return fmt.Errorf("text too long: estimated %d tokens, maximum %d tokens", estimatedTokens, maxTokens)
	}

	return nil
}
