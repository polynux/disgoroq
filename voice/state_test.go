package voice

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVoiceStateManagerIdleLifecycle(t *testing.T) {
	mgr := NewVoiceStateManager(40 * time.Millisecond)

	assert.True(t, mgr.CanProcess())
	assert.False(t, mgr.IsInIdle())

	mgr.MarkSpeechEnd()
	assert.True(t, mgr.IsInIdle())
	assert.False(t, mgr.CanProcess())

	mgr.BufferAudio([]byte{1, 2, 3})
	time.Sleep(50 * time.Millisecond)
	assert.True(t, mgr.CanProcess())
	assert.Equal(t, []byte{1, 2, 3}, mgr.GetIdleBuffer())
	assert.False(t, mgr.IsInIdle())
}

func TestVoiceStateManagerBufferAudioIgnoredOutsideIdle(t *testing.T) {
	mgr := NewVoiceStateManager(time.Second)
	mgr.BufferAudio([]byte{1, 2, 3})
	assert.Empty(t, mgr.GetIdleBuffer())
}

func TestVoiceStateManagerResetClearsState(t *testing.T) {
	mgr := NewVoiceStateManager(time.Second)
	mgr.MarkSpeechEnd()
	mgr.BufferAudio([]byte{1, 2, 3})
	mgr.Reset()

	assert.False(t, mgr.IsInIdle())
	assert.True(t, mgr.CanProcess())
	assert.Zero(t, mgr.TimeSinceLastSpeech())
	assert.Empty(t, mgr.GetIdleBuffer())
}
