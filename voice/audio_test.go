package voice

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pcmFrame(samples ...int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(sample))
	}
	return buf
}

func TestAudioBufferManagerTrimSilence(t *testing.T) {
	mgr := NewAudioBufferManager(1000, 1, 1)
	mgr.SetSilenceLevel(10)
	mgr.AddFrame(pcmFrame(0))
	mgr.AddFrame(pcmFrame(1000))
	mgr.AddFrame(pcmFrame(1200))
	mgr.AddFrame(pcmFrame(0))

	trimmed := mgr.TrimSilence()
	require.Len(t, trimmed, 4)
	assert.Equal(t, pcmFrame(1000, 1200), trimmed)
}

func TestAudioBufferManagerTrimSilenceAllSilent(t *testing.T) {
	mgr := NewAudioBufferManager(1000, 1, 1)
	mgr.SetSilenceLevel(10)
	mgr.AddFrame(pcmFrame(0))
	mgr.AddFrame(pcmFrame(0))

	assert.Equal(t, pcmFrame(0, 0), mgr.TrimSilence())
}

func TestAudioBufferManagerPrependAudioTracksSpeech(t *testing.T) {
	mgr := NewAudioBufferManager(1000, 1, 1)
	mgr.SetSilenceLevel(10)
	mgr.AddFrame(pcmFrame(0))
	mgr.AddFrame(pcmFrame(0))

	prepended := append(pcmFrame(900), pcmFrame(1100)...)
	mgr.PrependAudio(prepended)

	assert.True(t, mgr.HasSpeech())
	assert.Equal(t, 4, mgr.Duration())
	assert.Equal(t, 4, mgr.SpeechDuration())
	assert.Equal(t, append(prepended, prepended...), mgr.PeekAudio())
}

func TestConvertDiscordFormatsRoundTripMono(t *testing.T) {
	mono := append(pcmFrame(1000), pcmFrame(-1000)...)
	toDiscord, err := ConvertToDiscordFormat(mono, 48000, 1)
	require.NoError(t, err)
	assert.Len(t, toDiscord, len(mono)*2)

	fromDiscord, err := ConvertFromDiscordFormat(toDiscord, 48000, 1)
	require.NoError(t, err)
	assert.Len(t, fromDiscord, len(mono))
	assert.Equal(t, mono, fromDiscord)
}
