package main

import (
	"context"
	"strings"
	"testing"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/medialibrary"
	"polynux/disgoroq/memory"
)

type stubChatService struct {
	request *ai.ChatRequest
}

func (s *stubChatService) Chat(ctx context.Context, req *ai.ChatRequest) (*ai.ChatResponse, error) {
	s.request = req
	return &ai.ChatResponse{Content: "summary"}, nil
}

func (s *stubChatService) Vision(context.Context, *ai.VisionRequest) (*ai.VisionResponse, error) {
	return nil, nil
}

func (s *stubChatService) Name() string { return "stub" }

func (s *stubChatService) AvailableModels() []ai.ModelInfo { return nil }

func TestAIServiceAdapterDisablesToolsForSummaries(t *testing.T) {
	stub := &stubChatService{}
	adapter := &aiServiceAdapter{service: stub}

	_, err := adapter.Chat(context.Background(), []memory.Message{
		{Role: "system", Content: "summarize"},
		{Role: "user", Content: "conversation"},
	}, "test-model")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if stub.request == nil {
		t.Fatal("expected chat request to be captured")
	}
	if stub.request.ToolChoice == nil {
		t.Fatal("expected tool choice to be set")
	}
	if stub.request.ToolChoice.Mode != ai.ToolChoiceNone {
		t.Fatalf("ToolChoice.Mode = %q, want %q", stub.request.ToolChoice.Mode, ai.ToolChoiceNone)
	}
}

func TestBuildSummaryProviderConfig(t *testing.T) {
	t.Run("opencode falls back to ai.opencode.api_key and passes base_url", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.AI.Opencode.APIKey = "opencode-ai-key"
		cfg.Memory.SummaryProvider = config.SummaryProviderOpencode
		cfg.Memory.SummaryModel = "deepseek-v4.1-flash"

		got := buildSummaryProviderConfig(cfg)
		if got.APIKey != "opencode-ai-key" {
			t.Errorf("APIKey = %q, want opencode-ai-key", got.APIKey)
		}
		if got.OpencodeBaseURL != "https://opencode.ai/inference/go/v1" {
			t.Errorf("OpencodeBaseURL = %q, want https://opencode.ai/inference/go/v1", got.OpencodeBaseURL)
		}

		p, err := ai.NewSummaryProvider(got)
		if err != nil {
			t.Fatalf("NewSummaryProvider() error = %v", err)
		}
		if p.Name() != config.SummaryProviderOpencode {
			t.Errorf("provider Name() = %q, want %q", p.Name(), config.SummaryProviderOpencode)
		}
	})

	t.Run("opencode prefers memory.summary_api_key and keeps custom base_url", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.AI.Opencode.APIKey = "opencode-ai-key"
		cfg.AI.Opencode.BaseURL = "https://custom.opencode.example/v1"
		cfg.Memory.SummaryProvider = config.SummaryProviderOpencode
		cfg.Memory.SummaryModel = "deepseek-v4.1-flash"
		cfg.Memory.SummaryAPIKey = "dedicated-summary-key"

		got := buildSummaryProviderConfig(cfg)
		if got.APIKey != "dedicated-summary-key" {
			t.Errorf("APIKey = %q, want dedicated-summary-key", got.APIKey)
		}
		if got.OpencodeBaseURL != "https://custom.opencode.example/v1" {
			t.Errorf("OpencodeBaseURL = %q, want https://custom.opencode.example/v1", got.OpencodeBaseURL)
		}
	})

	t.Run("openrouter falls back to ai.openrouter.api_key without leaking opencode base_url", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.AI.Openrouter.APIKey = "openrouter-ai-key"
		cfg.Memory.SummaryProvider = config.SummaryProviderOpenrouter
		cfg.Memory.SummaryModel = "google/gemini-2.5-flash"

		got := buildSummaryProviderConfig(cfg)
		if got.APIKey != "openrouter-ai-key" {
			t.Errorf("APIKey = %q, want openrouter-ai-key", got.APIKey)
		}
		if got.OpencodeBaseURL != "" {
			t.Errorf("OpencodeBaseURL = %q, want empty for openrouter", got.OpencodeBaseURL)
		}
	})

	t.Run("groq falls back to ai.groq.api_key", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.AI.Groq.APIKey = "groq-ai-key"
		cfg.Memory.SummaryProvider = config.SummaryProviderGroq
		cfg.Memory.SummaryModel = "llama3-8b-8192"

		got := buildSummaryProviderConfig(cfg)
		if got.APIKey != "groq-ai-key" {
			t.Errorf("APIKey = %q, want groq-ai-key", got.APIKey)
		}
		if got.OpencodeBaseURL != "" {
			t.Errorf("OpencodeBaseURL = %q, want empty for groq", got.OpencodeBaseURL)
		}
	})
}

func TestResolveMediaEmbeddingConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Memory.OllamaURL = "http://memory-ollama:11434"
	cfg.Memory.EmbeddingModel = "custom-embed"
	cfg.AI.Ollama.URL = "http://ai-ollama:11434"

	url, model := resolveMediaEmbeddingConfig(cfg)
	if url != "http://memory-ollama:11434" || model != "custom-embed" {
		t.Fatalf("got (%q, %q), want (http://memory-ollama:11434, custom-embed)", url, model)
	}

	cfg.Memory.OllamaURL = ""
	cfg.Memory.EmbeddingModel = ""
	url, model = resolveMediaEmbeddingConfig(cfg)
	if url != "http://ai-ollama:11434" || model != "nomic-embed-text" {
		t.Fatalf("fallback got (%q, %q), want (http://ai-ollama:11434, nomic-embed-text)", url, model)
	}
}

func TestFormatMediaSearchResults(t *testing.T) {
	if got := formatMediaSearchResults(nil); got != "No matches found.\n" {
		t.Fatalf("empty results = %q", got)
	}
	out := formatMediaSearchResults([]medialibrary.SearchHit{
		{
			Similarity: 0.9123,
			Entry: medialibrary.Entry{
				Kind:        "gif",
				Uses:        7,
				Description: "cat smile",
				URL:         "https://tenor.com/view/cat-smile-1",
			},
		},
	})
	for _, want := range []string{"0.9123", "gif", "7", "cat smile", "https://tenor.com/view/cat-smile-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

