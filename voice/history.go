// Package voice provides voice chat capabilities for the Discord bot.
// It handles voice connections, speech-to-text, and text-to-speech integration.
package voice

import (
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// VoiceHistoryManager tracks conversation history per voice channel with speaker differentiation.
// It maintains a rolling window of messages for AI context building.
type VoiceHistoryManager struct {
	messages  []VoiceMessage
	maxLength int
	mu        sync.RWMutex
}

// NewVoiceHistoryManager creates a new voice history manager with the specified maximum length.
// When the history exceeds maxLength, oldest messages are trimmed.
func NewVoiceHistoryManager(maxLength int) *VoiceHistoryManager {
	return &VoiceHistoryManager{
		messages:  make([]VoiceMessage, 0, maxLength),
		maxLength: maxLength,
	}
}

// AddMessage adds a new message to the history.
// If the history exceeds maxLength, the oldest messages are trimmed.
func (h *VoiceHistoryManager) AddMessage(userID, username, content string, isBot bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	msg := VoiceMessage{
		UserID:    userID,
		Username:  username,
		Content:   content,
		IsBot:     isBot,
		Timestamp: time.Now(),
	}

	h.messages = append(h.messages, msg)

	logger.Debug("Added message to voice history",
		zap.String("user_id", userID),
		zap.String("username", username),
		zap.Bool("is_bot", isBot),
		zap.Int("total_messages", len(h.messages)),
	)

	// Trim if over max length
	if len(h.messages) > h.maxLength {
		trimmed := len(h.messages) - h.maxLength
		h.messages = h.messages[trimmed:]
		logger.Debug("Trimmed voice history",
			zap.Int("removed_count", trimmed),
			zap.Int("remaining_count", len(h.messages)),
		)
	}
}

// GetHistory returns a thread-safe copy of all messages in the history.
func (h *VoiceHistoryManager) GetHistory() []VoiceMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]VoiceMessage, len(h.messages))
	copy(result, h.messages)
	return result
}

// Clear removes all messages from the history.
func (h *VoiceHistoryManager) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.messages = h.messages[:0]
	logger.Debug("Voice history cleared")
}

// FormatForContext formats the messages for AI context as a string.
// Format:
//
//	[Username]: message content
//	[BotUsername]: bot response
func (h *VoiceHistoryManager) FormatForContext() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.messages) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, msg := range h.messages {
		sb.WriteString("[")
		sb.WriteString(msg.Username)
		sb.WriteString("]: ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}

	return sb.String()
}

// GetLastN returns the last N messages from the history.
// If n is greater than the history length, all messages are returned.
// If n <= 0, an empty slice is returned.
func (h *VoiceHistoryManager) GetLastN(n int) []VoiceMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if n <= 0 {
		return []VoiceMessage{}
	}

	if n >= len(h.messages) {
		result := make([]VoiceMessage, len(h.messages))
		copy(result, h.messages)
		return result
	}

	// Return last N messages
	start := len(h.messages) - n
	result := make([]VoiceMessage, n)
	copy(result, h.messages[start:])
	return result
}

// Len returns the current number of messages in the history.
func (h *VoiceHistoryManager) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.messages)
}
