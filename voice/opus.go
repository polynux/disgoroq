package voice

import (
	"sync"

	"go.uber.org/zap"
	"layeh.com/gopus"

	"polynux/disgoroq/logger"
)

// OpusDecodeSession manages Opus decoding for a voice session.
type OpusDecodeSession struct {
	decoders map[uint32]*gopus.Decoder // SSRC -> decoder
	mu       sync.Mutex
}

// NewOpusDecodeSession creates a new Opus decode session.
func NewOpusDecodeSession() *OpusDecodeSession {
	return &OpusDecodeSession{
		decoders: make(map[uint32]*gopus.Decoder),
	}
}

// DecodePacket decodes a raw Opus packet to PCM.
// The packet should be raw Opus data (after DAVE decryption by libdave).
// ssrc is optional for tracking multiple speakers.
func (s *OpusDecodeSession) DecodePacket(opusData []byte, ssrc uint32) ([]byte, error) {
	if len(opusData) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Get or create decoder for this SSRC
	decoder, exists := s.decoders[ssrc]
	if !exists {
		var err error
		decoder, err = gopus.NewDecoder(48000, 2) // Discord uses 48kHz stereo
		if err != nil {
			logger.Error("Failed to create Opus decoder",
				zap.Uint32("ssrc", ssrc),
				zap.Error(err))
			return nil, err
		}
		s.decoders[ssrc] = decoder
	}

	// Decode Opus to PCM
	// 960 samples = 20ms at 48kHz stereo
	pcm, err := decoder.Decode(opusData, 960, false)
	if err != nil {
		logger.Error("Failed to decode Opus packet",
			zap.Uint32("ssrc", ssrc),
			zap.Int("opus_len", len(opusData)),
			zap.Error(err))
		return nil, err
	}

	// Convert []int16 to []byte (little-endian)
	buf := make([]byte, len(pcm)*2)
	for i, sample := range pcm {
		buf[i*2] = byte(sample)
		buf[i*2+1] = byte(sample >> 8)
	}

	return buf, nil
}

// Close closes all decoders.
func (s *OpusDecodeSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// layeh.com/gopus doesn't have a Destroy method, decoders are garbage collected
	s.decoders = make(map[uint32]*gopus.Decoder)
}

// OpusEncoder encodes PCM audio to Opus for Discord.
type OpusEncoder struct {
	encoder    *gopus.Encoder
	sampleRate int
	channels   int
	frameSize  int // samples per frame (e.g., 960 for 20ms at 48kHz)
	mu         sync.Mutex
}

// NewOpusEncoder creates a new Opus encoder for Discord voice.
// Discord requires 48kHz stereo Opus audio.
func NewOpusEncoder(frameSize int) (*OpusEncoder, error) {
	// Discord uses 48kHz stereo
	encoder, err := gopus.NewEncoder(48000, 2, gopus.Audio)
	if err != nil {
		return nil, err
	}

	if frameSize <= 0 {
		frameSize = 960
	}

	return &OpusEncoder{
		encoder:    encoder,
		sampleRate: 48000,
		channels:   2,
		frameSize:  frameSize,
	}, nil
}

// Encode encodes PCM audio to Opus frames.
// Input must be 48kHz stereo 16-bit PCM.
// Returns a slice of Opus frames, each frame is 20ms of audio.
func (e *OpusEncoder) Encode(pcm []byte) ([][]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Convert []byte to []int16
	samples := len(pcm) / 2 // 16-bit samples
	pcmInt16 := make([]int16, samples)
	for i := 0; i < samples; i++ {
		pcmInt16[i] = int16(uint16(pcm[i*2]) | uint16(pcm[i*2+1])<<8)
	}

	// Split into frames (960 samples per frame for 20ms at 48kHz stereo)
	// For stereo, each frame needs samplesPerFrame * channels samples
	samplesPerFrame := e.frameSize
	samplesNeeded := samplesPerFrame * e.channels
	var frames [][]byte

	for i := 0; i < len(pcmInt16); i += samplesNeeded {
		end := i + samplesNeeded
		var frameSamples []int16
		if end > len(pcmInt16) {
			// Pad last frame with silence
			frameSamples = make([]int16, samplesNeeded)
			copy(frameSamples, pcmInt16[i:])
		} else {
			frameSamples = pcmInt16[i:end]
		}

		// Encode frame
		// maxBytesPerFrame: recommended ~4000 bytes for Opus
		opus, err := e.encoder.Encode(frameSamples, samplesPerFrame, 4000)
		if err != nil {
			return nil, err
		}
		frames = append(frames, opus)
	}

	return frames, nil
}
