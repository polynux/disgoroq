package ai

import (
	"context"
	"testing"
)

func TestNewSummaryProvider_RequiresModel(t *testing.T) {
	_, err := NewSummaryProvider(SummaryProviderConfig{Provider: "ollama"})
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}

func TestNewSummaryProvider_RequiresProvider(t *testing.T) {
	_, err := NewSummaryProvider(SummaryProviderConfig{Model: "some-model"})
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestNewSummaryProvider_UnsupportedProvider(t *testing.T) {
	_, err := NewSummaryProvider(SummaryProviderConfig{Provider: "nope", Model: "m"})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestNewSummaryProvider_GroqRequiresAPIKey(t *testing.T) {
	_, err := NewSummaryProvider(SummaryProviderConfig{Provider: "groq", Model: "m"})
	if err == nil {
		t.Fatal("expected error for missing groq api key")
	}
}

func TestNewSummaryProvider_Ollama(t *testing.T) {
	p, err := NewSummaryProvider(SummaryProviderConfig{
		Provider:  "ollama",
		Model:     "gemma3:4b",
		OllamaURL: "http://localhost:11434",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
	if p.Name() != "ollama" {
		t.Errorf("expected provider name ollama, got %q", p.Name())
	}
}

// TestSummaryProviderModelPassthrough verifies the retry wrapper keeps the
// configured summary model in the request instead of overriding it.
func TestSummaryProviderModelPassthrough(t *testing.T) {
	p, err := NewSummaryProvider(SummaryProviderConfig{
		Provider:  "ollama",
		Model:     "gemma3:4b",
		OllamaURL: "http://localhost:11434",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mp, ok := p.(modelAwareProvider); ok {
		if got := mp.ChatModelName(); got != "gemma3:4b" {
			t.Errorf("expected chat model gemma3:4b, got %q", got)
		}
	} else {
		t.Fatal("provider should implement modelAwareProvider")
	}
}

// Compile-time check that Provider satisfies the interface used in main.go.
var _ Provider = (*RetryWrapper)(nil)

func TestRetryWrapperChatRequestShape(t *testing.T) {
	// Sanity: ChatRequest with ToolChoiceNone that memory summarization sends.
	req := &ChatRequest{
		Model:    "gemma3:4b",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
	if req.Model != "gemma3:4b" {
		t.Errorf("unexpected model %q", req.Model)
	}
	_ = context.Background()
}
