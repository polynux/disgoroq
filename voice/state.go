package voice

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// VoiceStateManager handles idle timeout and audio buffering during the pause
// between AI responses. This allows the bot to buffer incoming audio while
// waiting for the idle timeout to expire, so context is not lost during pauses.
type VoiceStateManager struct {
	// lastSpeechEnd tracks when the AI finished speaking
	lastSpeechEnd time.Time

	// idleTimeout is the duration to wait after speech ends before processing
	idleTimeout time.Duration

	// idleBuffer accumulates audio received during the idle period
	idleBuffer []byte

	// inIdle tracks whether we're currently in an idle timeout period
	inIdle bool

	// mu protects concurrent access to state
	mu sync.RWMutex
}

// NewVoiceStateManager creates a new voice state manager with the specified idle timeout.
func NewVoiceStateManager(idleTimeout time.Duration) *VoiceStateManager {
	return &VoiceStateManager{
		idleTimeout: idleTimeout,
		idleBuffer:  make([]byte, 0, 4096), // Pre-allocate buffer for efficiency
	}
}

// MarkSpeechEnd marks when the AI finishes speaking and starts the idle period.
// This should be called immediately after TTS audio playback completes.
func (m *VoiceStateManager) MarkSpeechEnd() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastSpeechEnd = time.Now()
	wasInIdle := m.inIdle
	m.inIdle = true

	logger.Info("Entered idle state",
		zap.Duration("timeout", m.idleTimeout),
		zap.Bool("previously_in_idle", wasInIdle))
}

// IsInIdle returns true if currently in the idle timeout period.
func (m *VoiceStateManager) IsInIdle() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inIdle
}

// CanProcess returns true if the idle timeout has passed and new audio can be processed.
// This indicates that the bot is ready to listen for new user input.
func (m *VoiceStateManager) CanProcess() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.inIdle {
		return true
	}

	// Check if idle timeout has elapsed
	elapsed := time.Since(m.lastSpeechEnd)
	return elapsed >= m.idleTimeout
}

// BufferAudio adds audio data to the idle buffer during the idle period.
// This audio will be transcribed after the idle timeout expires, preserving
// context of what was said during the pause.
func (m *VoiceStateManager) BufferAudio(audio []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.inIdle {
		// Not in idle period, should not buffer
		logger.Debug("Attempted to buffer audio outside idle period, ignoring",
			zap.Int("audio_bytes", len(audio)))
		return
	}

	m.idleBuffer = append(m.idleBuffer, audio...)
	logger.Debug("Buffered audio during idle period",
		zap.Int("added_bytes", len(audio)),
		zap.Int("total_buffered", len(m.idleBuffer)))
}

// GetIdleBuffer returns the buffered audio and clears the buffer.
// This should be called when transitioning out of idle state to get
// any audio that was spoken during the pause.
func (m *VoiceStateManager) GetIdleBuffer() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	buffer := m.idleBuffer
	m.idleBuffer = make([]byte, 0, 4096) // Reset buffer with pre-allocated capacity

	// Exit idle state
	m.inIdle = false

	// Only log if there was actual buffered audio
	if len(buffer) > 0 {
		logger.Info("Exiting idle state, returning buffered audio",
			zap.Int("buffer_bytes", len(buffer)))
	}

	return buffer
}

// ClearIdleBuffer clears the idle buffer without returning it.
// Use this to discard buffered audio without processing it.
func (m *VoiceStateManager) ClearIdleBuffer() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.idleBuffer) > 0 {
		logger.Debug("Clearing idle buffer",
			zap.Int("discarded_bytes", len(m.idleBuffer)))
	}

	m.idleBuffer = make([]byte, 0, 4096)
	m.inIdle = false
}

// TimeSinceLastSpeech returns the duration since the AI finished speaking.
// Returns 0 if not in idle state or if last speech end has not been set.
func (m *VoiceStateManager) TimeSinceLastSpeech() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.lastSpeechEnd.IsZero() {
		return 0
	}

	return time.Since(m.lastSpeechEnd)
}

// Reset clears all state, including the idle buffer and tracking.
// This should be called when leaving a voice channel or resetting conversation.
func (m *VoiceStateManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastSpeechEnd = time.Time{}
	m.idleBuffer = make([]byte, 0, 4096)
	m.inIdle = false

	logger.Info("Voice state manager reset")
}
