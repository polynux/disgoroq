package main

import (
	"context"
	"testing"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/memory"
)

type stubChatService struct {
	request *ai.ChatRequest
}

func (s *stubChatService) Chat(ctx context.Context, req *ai.ChatRequest) (*ai.ChatResponse, error) {
	s.request = req
	return &ai.ChatResponse{Content: "summary"}, nil
}

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
