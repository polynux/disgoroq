package handlers

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"polynux/disgoroq/ai"
)

func TestTryAcquireReplySlotIsBounded(t *testing.T) {
	h := &MessageHandler{replySlots: make(chan struct{}, 1)}

	if !h.tryAcquireReplySlot() {
		t.Fatal("expected the first reply slot acquisition to succeed")
	}
	if h.tryAcquireReplySlot() {
		t.Fatal("expected acquisition to fail while the slot is occupied")
	}

	<-h.replySlots
	if !h.tryAcquireReplySlot() {
		t.Fatal("expected acquisition to succeed after the slot is released")
	}
}

func TestLockForSharesOneLockPerKey(t *testing.T) {
	h := &MessageHandler{}

	first := h.lockFor(&h.replyLocks, "1")
	if first != h.lockFor(&h.replyLocks, "1") {
		t.Fatal("expected the same key to share a lock")
	}
	if first == h.lockFor(&h.replyLocks, "2") {
		t.Fatal("expected different keys to use separate locks")
	}
}

func TestChatSessionID(t *testing.T) {
	guildID := snowflake.ID(123456789)
	channelID := snowflake.ID(987654321)

	if got := chatSessionID(&guildID, channelID); got != "123456789:987654321" {
		t.Fatalf("chatSessionID(guild, channel) = %q, want %q", got, "123456789:987654321")
	}
	if got := chatSessionID(nil, channelID); got != "987654321" {
		t.Fatalf("chatSessionID(nil, channel) = %q, want %q", got, "987654321")
	}
	if got := chatSessionID(&guildID, 0); got != "" {
		t.Fatalf("chatSessionID(guild, 0) = %q, want empty", got)
	}
}

func TestSkipCompletionTarget(t *testing.T) {
	newService := func(completion bool) *ai.Service {
		return ai.NewService(ai.ServiceConfig{
			PrimaryProvider:          ai.ProviderOllama,
			OllamaEnabled:            true,
			OllamaURL:                "http://localhost:11434",
			OllamaModel:              "luciole-1b-discord1",
			OllamaVisionModel:        "llava",
			OllamaCompletion:         completion,
			RetryConfig:              ai.DefaultRetryConfig(),
			MinResponseLength:        1,
			OllamaCompletionBotLines: 4,
		})
	}

	botMessage := discord.Message{Author: discord.User{ID: snowflake.ID(1), Bot: true}}
	webhookID := snowflake.ID(2)
	webhookMessage := discord.Message{WebhookID: &webhookID}
	humanMessage := discord.Message{Author: discord.User{ID: snowflake.ID(3)}}

	completionHandler := &MessageHandler{aiservice: newService(true)}
	if !completionHandler.skipCompletionTarget(botMessage) {
		t.Fatal("completion mode must skip another bot's message")
	}
	if !completionHandler.skipCompletionTarget(webhookMessage) {
		t.Fatal("completion mode must skip a webhook message")
	}
	if completionHandler.skipCompletionTarget(humanMessage) {
		t.Fatal("completion mode must answer a human message")
	}

	chatHandler := &MessageHandler{aiservice: newService(false)}
	if chatHandler.skipCompletionTarget(botMessage) {
		t.Fatal("chat mode must keep its current bot behaviour")
	}
}
