package voice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"polynux/disgoroq/config"
)

type testVoiceRepo struct {
	voiceEnabled bool
}

func (r testVoiceRepo) GetVoiceEnabled(ctx context.Context, guildID string) bool {
	return r.voiceEnabled
}
func (r testVoiceRepo) GetTemperature(ctx context.Context, guildID string) float32 { return 0 }
func (r testVoiceRepo) GetPrompt(ctx context.Context, guildID string) (string, bool) {
	return "", false
}
func (r testVoiceRepo) GetVoicePrompt(ctx context.Context, guildID string) (string, bool) {
	return "", false
}

func TestOrchestratorEnsureTTSLoadedSkipsWhenAlreadyLoaded(t *testing.T) {
	tts := &MockTTSClient{ModelLoaded: true}
	o := &Orchestrator{ttsClient: tts}
	require.NoError(t, o.ensureTTSLoaded(context.Background()))
	assert.True(t, tts.ModelLoaded)
}

func TestOrchestratorEnsureTTSLoadedChecksVRAM(t *testing.T) {
	tts := &MockTTSClient{VRAM: 1000}
	o := &Orchestrator{
		ttsClient: tts,
		config:    config.VoiceConfig{VRAM: config.VRAMConfig{AutoUnload: true, MinFreeMB: 1500}},
	}

	err := o.ensureTTSLoaded(context.Background())
	assert.ErrorIs(t, err, ErrInsufficientVRAM)
	assert.False(t, tts.ModelLoaded)
}

func TestOrchestratorEnsureTTSLoadedLoadsWhenVRAMCheckFails(t *testing.T) {
	tts := &MockTTSClient{Error: errors.New("vram unavailable")}
	o := &Orchestrator{
		ttsClient: tts,
		config:    config.VoiceConfig{VRAM: config.VRAMConfig{AutoUnload: true, MinFreeMB: 1500}},
	}

	err := o.ensureTTSLoaded(context.Background())
	assert.Error(t, err)
	assert.True(t, tts.ModelLoaded)

	tts.Error = nil
	tts.VRAM = 2000
	tts.ModelLoaded = false
	err = o.ensureTTSLoaded(context.Background())
	require.NoError(t, err)
	assert.True(t, tts.ModelLoaded)
}

func TestOrchestratorMaybeUnloadTTS(t *testing.T) {
	tts := &MockTTSClient{ModelLoaded: true}
	o := &Orchestrator{
		ttsClient:    tts,
		sessions:     map[string]*VoiceConversation{},
		lastTTSAudio: time.Now().Add(-2 * time.Second),
		config:       config.VoiceConfig{VRAM: config.VRAMConfig{UnloadTimeoutSeconds: 1}},
	}

	o.maybeUnloadTTS()
	assert.False(t, tts.ModelLoaded)
}

func TestOrchestratorLimitTTSText(t *testing.T) {
	o := &Orchestrator{config: config.VoiceConfig{TTS: config.TTSConfig{MaxTextLength: 20}}}
	assert.Equal(t, "short", o.limitTTSText("short"))
	assert.Equal(t, "hello there", o.limitTTSText("hello there general kenobi friend"))
}

func TestOrchestratorResolveSpeakerID(t *testing.T) {
	o := &Orchestrator{}
	conv := &VoiceConversation{LastSpeakerID: "fallback-user"}
	assert.Equal(t, "explicit-user", o.resolveSpeakerID(conv, "explicit-user"))
	assert.Equal(t, "fallback-user", o.resolveSpeakerID(conv, ""))
}
