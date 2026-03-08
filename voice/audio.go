package voice

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// AudioProcessor handles audio encoding/decoding for Discord voice.
type AudioProcessor struct {
	sampleRate int
	channels   int
	frameSize  int // samples per frame
}

// NewAudioProcessor creates a new audio processor.
func NewAudioProcessor(sampleRate, channels, frameSize int) *AudioProcessor {
	return &AudioProcessor{
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
	}
}

// FrameDuration returns the duration of a single audio frame.
func (p *AudioProcessor) FrameDuration() int {
	// Duration in milliseconds = (samples / sampleRate) * 1000
	return (p.frameSize * 1000) / p.sampleRate
}

// BytesPerFrame returns the number of bytes per audio frame.
func (p *AudioProcessor) BytesPerFrame() int {
	return p.frameSize * p.channels * 2 // 2 bytes per sample (16-bit)
}

// FrameToPCM converts an Opus frame to raw PCM.
// Discord uses 48kHz stereo by default.
func (p *AudioProcessor) FrameToPCM(opusFrame []byte) ([]byte, error) {
	// For now, we return the frame as-is since we're working with
	// pre-decoded PCM from whisper.cpp
	// In a full implementation, you'd use gopus to decode
	return opusFrame, nil
}

// PCMToFrame converts raw PCM to an Opus frame.
func (p *AudioProcessor) PCMToFrame(pcm []byte) ([]byte, error) {
	// For now, we return the PCM as-is since the TTS service
	// provides pre-encoded Opus or we send PCM directly
	// In a full implementation, you'd use gopus to encode
	return pcm, nil
}

// Resample converts audio from one sample rate to another.
// For stereo audio (channels=2), L and R channels are interpolated separately.
// For production use, consider using a proper resampling library.
func Resample(input []byte, inRate, outRate, channels int) []byte {
	if inRate == outRate {
		return input
	}

	// Number of samples per channel
	samplesPerChannel := len(input) / (2 * channels)
	// Output samples per channel
	outputSamplesPerChannel := samplesPerChannel * outRate / inRate

	// Total output bytes
	output := make([]byte, outputSamplesPerChannel*2*channels)

	ratio := float64(inRate) / float64(outRate)

	// Process each channel separately
	for ch := 0; ch < channels; ch++ {
		for i := 0; i < outputSamplesPerChannel; i++ {
			srcPos := float64(i) * ratio
			srcIndex := int(srcPos)

			// Read and interpolate for this channel
			if srcIndex+1 < samplesPerChannel {
				frac := srcPos - float64(srcIndex)

				// Get source indices for this channel
				idx1 := (srcIndex*channels + ch) * 2
				idx2 := ((srcIndex+1)*channels + ch) * 2

				s1 := int16(binary.LittleEndian.Uint16(input[idx1:]))
				s2 := int16(binary.LittleEndian.Uint16(input[idx2:]))

				// Interpolate
				result := s1 + int16(float64(s2-s1)*frac)

				// Write to output at correct position for this channel
				outIdx := (i*channels + ch) * 2
				binary.LittleEndian.PutUint16(output[outIdx:], uint16(result))
			} else if srcIndex < samplesPerChannel {
				// Last sample for this channel
				idx := (srcIndex*channels + ch) * 2
				s := binary.LittleEndian.Uint16(input[idx:])
				outIdx := (i*channels + ch) * 2
				binary.LittleEndian.PutUint16(output[outIdx:], s)
			}
		}
	}

	return output
}

// StereoToMono converts stereo audio to mono by averaging channels.
func StereoToMono(input []byte) []byte {
	// Input is interleaved stereo: L0 R0 L1 R1 ...
	// Output is mono: M0 M1 ...
	samples := len(input) / 4 // 2 channels * 2 bytes per sample
	output := make([]byte, samples*2)

	for i := 0; i < samples; i++ {
		// Read left and right samples
		l := int16(binary.LittleEndian.Uint16(input[i*4:]))
		r := int16(binary.LittleEndian.Uint16(input[i*4+2:]))

		// Average them
		m := int16((int32(l) + int32(r)) / 2)

		// Write mono sample
		binary.LittleEndian.PutUint16(output[i*2:], uint16(m))
	}

	return output
}

// MonoToStereo converts mono audio to stereo by duplicating the channel.
func MonoToStereo(input []byte) []byte {
	samples := len(input) / 2
	output := make([]byte, samples*4)

	for i := 0; i < samples; i++ {
		s := binary.LittleEndian.Uint16(input[i*2:])
		// Write to both channels
		binary.LittleEndian.PutUint16(output[i*4:], s)
		binary.LittleEndian.PutUint16(output[i*4+2:], s)
	}

	return output
}

// AudioBufferManager manages audio buffering for transcription.
// It accumulates audio frames and provides silence detection with VAD support.
type AudioBufferManager struct {
	buffer           *bytes.Buffer
	sampleRate       int
	channels         int
	frameMs          int
	totalMs          int
	silenceLevel     int16     // Amplitude threshold for silence detection
	speechStartMs    int       // When speech started (for VAD)
	hasSpeech        bool      // Whether buffer contains speech
	speechDurationMs int       // Total speech duration
	lastAudioTime    time.Time // Last time we received audio (for silence detection)
	silenceStartMs   int       // When silence started (relative to totalMs)
}

// NewAudioBufferManager creates a new audio buffer manager.
func NewAudioBufferManager(sampleRate, channels, frameMs int) *AudioBufferManager {
	return &AudioBufferManager{
		buffer:       bytes.NewBuffer(nil),
		sampleRate:   sampleRate,
		channels:     channels,
		frameMs:      frameMs,
		silenceLevel: 500, // Default silence threshold
	}
}

// AddFrame adds an audio frame to the buffer.
func (m *AudioBufferManager) AddFrame(frame []byte) {
	m.buffer.Write(frame)
	m.totalMs += m.frameMs

	// Check for silence
	if m.isSilent(frame) {
		// Mark start of silence if this is first silent frame
		if m.silenceStartMs == 0 {
			m.silenceStartMs = m.totalMs
		}
	} else {
		// Frame has speech - reset silence tracking
		m.silenceStartMs = 0
		// Track start of speech
		if !m.hasSpeech {
			m.speechStartMs = m.totalMs - m.frameMs
			m.hasSpeech = true
		}
		m.speechDurationMs += m.frameMs
	}

	// Update last audio time
	m.lastAudioTime = time.Now()
}

// isSilent checks if a frame is silent (below threshold).
func (m *AudioBufferManager) isSilent(frame []byte) bool {
	if len(frame) < 2 {
		return true
	}

	// Find peak amplitude
	var maxSample int16
	for i := 0; i < len(frame)-1; i += 2 {
		sample := int16(binary.LittleEndian.Uint16(frame[i:]))
		absSample := sample
		if absSample < 0 {
			absSample = -absSample
		}
		if absSample > maxSample {
			maxSample = absSample
		}
	}

	// Frame is silent if peak amplitude is below threshold
	return maxSample < m.silenceLevel
}

// SilenceDuration returns the duration of consecutive silence in milliseconds.
// This is based on consecutive silent frames, not cumulative silence.
func (m *AudioBufferManager) SilenceDuration() int {
	if m.silenceStartMs == 0 {
		return 0
	}
	return m.totalMs - m.silenceStartMs
}

// SilenceDurationFromTime returns the duration of silence based on wall clock time.
// This is useful for detecting when Discord stops sending packets (true silence).
func (m *AudioBufferManager) SilenceDurationFromTime() time.Duration {
	if m.lastAudioTime.IsZero() {
		return 0
	}
	return time.Since(m.lastAudioTime)
}

// HasSpeech returns true if the buffer contains non-silent audio.
func (m *AudioBufferManager) HasSpeech() bool {
	return m.hasSpeech
}

// SpeechDuration returns the duration of speech in milliseconds.
func (m *AudioBufferManager) SpeechDuration() int {
	return m.speechDurationMs
}

// ConsecutiveSilentFrames returns the number of consecutive silent frames.
func (m *AudioBufferManager) ConsecutiveSilentFrames() int {
	if m.silenceStartMs == 0 {
		return 0
	}
	return (m.totalMs - m.silenceStartMs) / m.frameMs
}

// ConsecutiveSilentMs returns the duration of consecutive silence in milliseconds.
func (m *AudioBufferManager) ConsecutiveSilentMs() int {
	return m.SilenceDuration()
}

// TimeSinceLastAudio returns the time elapsed since the last audio frame was received.
// This is more reliable than frame counting because Discord doesn't send packets during silence.
func (m *AudioBufferManager) TimeSinceLastAudio() time.Duration {
	if m.lastAudioTime.IsZero() {
		return 0
	}
	return time.Since(m.lastAudioTime)
}

// GetAudio returns the buffered audio and clears the buffer.
func (m *AudioBufferManager) GetAudio() []byte {
	audio := m.buffer.Bytes()
	m.buffer.Reset()
	m.totalMs = 0
	m.speechStartMs = 0
	m.hasSpeech = false
	m.speechDurationMs = 0
	m.silenceStartMs = 0
	m.lastAudioTime = time.Time{}
	return audio
}

// GetTrimmedAudio returns the buffered audio with silence trimmed from start and end,
// then clears the buffer. This prevents Whisper from hallucinating on pure silence.
func (m *AudioBufferManager) GetTrimmedAudio() []byte {
	trimmed := m.TrimSilence()
	m.buffer.Reset()
	m.totalMs = 0
	m.speechStartMs = 0
	m.hasSpeech = false
	m.speechDurationMs = 0
	m.silenceStartMs = 0
	m.lastAudioTime = time.Time{}
	return trimmed
}

// PeekAudio returns the buffered audio without clearing.
func (m *AudioBufferManager) PeekAudio() []byte {
	return m.buffer.Bytes()
}

// Duration returns the total duration of buffered audio in milliseconds.
func (m *AudioBufferManager) Duration() int {
	return m.totalMs
}

// Clear clears the buffer.
func (m *AudioBufferManager) Clear() {
	m.buffer.Reset()
	m.totalMs = 0
	m.speechStartMs = 0
	m.hasSpeech = false
	m.speechDurationMs = 0
	m.silenceStartMs = 0
	m.lastAudioTime = time.Time{}
}

// TrimSilence removes silent frames from the beginning and end of the audio buffer.
// This prevents Whisper from hallucinating on pure silence (common issue with "Sous-titres..." artifacts).
// Returns the trimmed audio data.
func (m *AudioBufferManager) TrimSilence() []byte {
	audio := m.buffer.Bytes()
	if len(audio) < 2 {
		return audio
	}

	// Frame size in bytes: 2 bytes per sample * channels
	frameBytes := 2 * m.channels

	// Find first non-silent frame from start
	startFrame := 0
	for i := 0; i < len(audio)-frameBytes; i += frameBytes {
		frame := audio[i : i+frameBytes]
		if !m.isSilent(frame) {
			startFrame = i / frameBytes
			break
		}
	}

	// Find last non-silent frame from end
	endFrame := len(audio) / frameBytes
	for i := len(audio) - frameBytes; i >= 0; i -= frameBytes {
		if i+frameBytes > len(audio) {
			continue
		}
		frame := audio[i : i+frameBytes]
		if !m.isSilent(frame) {
			endFrame = (i / frameBytes) + 1
			break
		}
	}

	// If entirely silent or start >= end, return empty
	if startFrame >= endFrame {
		return nil
	}

	// Extract trimmed audio
	trimmedStart := startFrame * frameBytes
	trimmedEnd := endFrame * frameBytes

	logger.Debug("Trimmed silence from audio",
		zap.Int("original_bytes", len(audio)),
		zap.Int("trimmed_bytes", trimmedEnd-trimmedStart),
		zap.Int("frames_removed_start", startFrame),
		zap.Int("frames_removed_end", (len(audio)/frameBytes)-endFrame))

	return audio[trimmedStart:trimmedEnd]
}

// PrependAudio adds audio to the beginning of the buffer.
// This is useful for prepending idle-buffered audio to get context
// of what was said during the pause.
func (m *AudioBufferManager) PrependAudio(audio []byte) {
	if len(audio) == 0 {
		return
	}

	// Analyze prepended audio for speech content
	frameBytes := 2 * m.channels
	hasSpeechInPrepended := false
	for i := 0; i < len(audio)-frameBytes; i += frameBytes {
		frame := audio[i : i+frameBytes]
		if !m.isSilent(frame) {
			hasSpeechInPrepended = true
			break
		}
	}

	// Get current buffer content
	current := m.buffer.Bytes()

	// Create new buffer with prepended content
	m.buffer.Reset()
	m.buffer.Write(audio)
	m.buffer.Write(current)

	// Update duration tracking
	framesAdded := len(audio) / frameBytes
	msAdded := (framesAdded * 1000) / m.sampleRate
	m.totalMs += msAdded

	// If prepended audio has speech, mark it
	if hasSpeechInPrepended && !m.hasSpeech {
		m.hasSpeech = true
		m.speechStartMs = 0 // Speech starts at the beginning of prepended audio
	}

	// Update lastAudioTime to now since we just received audio
	m.lastAudioTime = time.Now()

	// Reset speech duration tracking - we'll recalculate on next analysis
	m.speechDurationMs = 0
	m.silenceStartMs = 0

	// Recalculate speech duration for all content
	for i := 0; i < m.totalMs; i += m.frameMs {
		frameIdx := (i / m.frameMs) * frameBytes
		if frameIdx+frameBytes <= m.buffer.Len() {
			frame := m.buffer.Bytes()[frameIdx : frameIdx+frameBytes]
			if !m.isSilent(frame) {
				m.speechDurationMs += m.frameMs
			}
		}
	}
}

// SetSilenceLevel sets the amplitude threshold for silence detection.
func (m *AudioBufferManager) SetSilenceLevel(level int16) {
	m.silenceLevel = level
}

// GetSilenceLevel returns the current silence level threshold.
func (m *AudioBufferManager) GetSilenceLevel() int16 {
	return m.silenceLevel
}

// SetSilenceThresholdFromNormalized sets the silence threshold from a normalized 0.0-1.0 value.
// Values like 0.02 (2% of max amplitude) are good for voice detection.
func (m *AudioBufferManager) SetSilenceThresholdFromNormalized(threshold float64) {
	// Convert normalized threshold (0.0-1.0) to int16 amplitude
	// threshold of 0.02 -> ~655 (0.02 * 32767)
	if threshold < 0.0 {
		threshold = 0.0
	}
	if threshold > 1.0 {
		threshold = 1.0
	}
	m.silenceLevel = int16(threshold * 32767.0)
}

// DetectVoiceActivity performs Voice Activity Detection (VAD) on audio.
// Returns true if speech is detected.
func DetectVoiceActivity(audio []byte, threshold float64) bool {
	if len(audio) < 2 {
		return false
	}

	// Calculate RMS energy
	var sumSquares float64
	samples := len(audio) / 2

	for i := 0; i < samples; i++ {
		sample := float64(int16(binary.LittleEndian.Uint16(audio[i*2:])))
		sumSquares += sample * sample
	}

	rms := sqrt(sumSquares / float64(samples))

	// Normalize to 0-1 range (assuming 16-bit audio)
	normalizedRms := rms / 32767.0

	return normalizedRms > threshold
}

// sqrt is a simple square root function to avoid importing math.
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}

	// Newton's method
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// ReadAudioStream reads audio from a reader and chunks it into frames.
func ReadAudioStream(r io.Reader, frameSize int) ([][]byte, error) {
	var frames [][]byte
	buf := make([]byte, frameSize)

	for {
		n, err := io.ReadFull(r, buf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				if n > 0 {
					// Pad last frame
					padded := make([]byte, frameSize)
					copy(padded, buf[:n])
					frames = append(frames, padded)
				}
				break
			}
			return nil, err
		}
		frames = append(frames, buf)
	}

	return frames, nil
}

// ConvertToDiscordFormat converts audio to Discord's expected format.
// Discord expects 48kHz stereo 16-bit PCM (or Opus encoded).
func ConvertToDiscordFormat(input []byte, inSampleRate, inChannels int) ([]byte, error) {
	output := input
	channels := inChannels

	// Convert mono to stereo if needed
	if inChannels == 1 {
		output = MonoToStereo(output)
		channels = 2
	}

	// Resample to 48kHz if needed
	if inSampleRate != 48000 {
		output = Resample(output, inSampleRate, 48000, channels)
	}

	return output, nil
}

// ConvertFromDiscordFormat converts audio from Discord's format.
// Useful for sending to STT services that may expect different formats.
func ConvertFromDiscordFormat(input []byte, outSampleRate, outChannels int) ([]byte, error) {
	output := input
	channels := 2 // Discord is always stereo

	// Resample to target rate if needed
	if outSampleRate != 48000 {
		output = Resample(output, 48000, outSampleRate, channels)
		channels = 2 // Still stereo after resample
	}

	// Convert stereo to mono if needed
	if outChannels == 1 {
		output = StereoToMono(output)
	}

	return output, nil
}

// WAVInfo contains parsed WAV file information.
type WAVInfo struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
	DataOffset    int
	DataSize      int
}

// ParseWAV extracts raw PCM data from WAV bytes and returns the PCM data along with audio info.
// It handles standard WAV files with proper RIFF headers.
func ParseWAV(wavData []byte) (pcmData []byte, info WAVInfo, err error) {
	if len(wavData) < 44 {
		return nil, WAVInfo{}, fmt.Errorf("invalid WAV data: too short (%d bytes)", len(wavData))
	}

	// Verify RIFF header
	if string(wavData[0:4]) != "RIFF" {
		return nil, WAVInfo{}, fmt.Errorf("invalid WAV: missing RIFF header, got: %s",
			hex.EncodeToString(wavData[0:4]))
	}

	// Verify WAVE format
	if string(wavData[8:12]) != "WAVE" {
		return nil, WAVInfo{}, fmt.Errorf("invalid WAV: missing WAVE format, got: %s",
			hex.EncodeToString(wavData[8:12]))
	}

	// Parse format chunk
	var formatFound bool
	offset := 12

	for offset < len(wavData)-8 {
		chunkID := string(wavData[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(wavData[offset+4 : offset+8]))

		if chunkID == "fmt " {
			if offset+8+chunkSize > len(wavData) {
				return nil, WAVInfo{}, fmt.Errorf("invalid WAV: fmt chunk extends beyond data")
			}

			// Parse format
			audioFormat := binary.LittleEndian.Uint16(wavData[offset+8 : offset+10])
			if audioFormat != 1 {
				return nil, WAVInfo{}, fmt.Errorf("unsupported WAV format: %d (only PCM supported)", audioFormat)
			}

			info.Channels = int(binary.LittleEndian.Uint16(wavData[offset+10 : offset+12]))
			info.SampleRate = int(binary.LittleEndian.Uint32(wavData[offset+12 : offset+16]))
			info.BitsPerSample = int(binary.LittleEndian.Uint16(wavData[offset+22 : offset+24]))
			formatFound = true

			logger.Debug("WAV format parsed",
				zap.Int("channels", info.Channels),
				zap.Int("sample_rate", info.SampleRate),
				zap.Int("bits", info.BitsPerSample))
		}

		if chunkID == "data" {
			info.DataOffset = offset + 8
			info.DataSize = chunkSize
			break
		}

		offset += 8 + chunkSize
		// Ensure word alignment
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if !formatFound {
		return nil, WAVInfo{}, fmt.Errorf("invalid WAV: no fmt chunk found")
	}

	if info.DataOffset == 0 {
		return nil, WAVInfo{}, fmt.Errorf("invalid WAV: no data chunk found")
	}

	// Extract PCM data
	if info.DataOffset+info.DataSize > len(wavData) {
		// Some WAV files have incorrect data size, use remaining data
		info.DataSize = len(wavData) - info.DataOffset
	}

	pcmData = wavData[info.DataOffset : info.DataOffset+info.DataSize]

	logger.Debug("WAV parsed successfully",
		zap.Int("pcm_size", len(pcmData)),
		zap.Int("sample_rate", info.SampleRate),
		zap.Int("channels", info.Channels))

	return pcmData, info, nil
}

// IsWAV checks if the given data appears to be a WAV file.
func IsWAV(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

// VoiceSpeakingHandler handles Discord voice speaking events.
// Note: VoiceSpeakingUpdate doesn't include GuildID, so it must be provided separately.
type VoiceSpeakingHandler struct {
	manager *Manager
	guildID string
}

// NewVoiceSpeakingHandler creates a new voice speaking handler.
func NewVoiceSpeakingHandler(manager *Manager, guildID string) *VoiceSpeakingHandler {
	return &VoiceSpeakingHandler{
		manager: manager,
		guildID: guildID,
	}
}

// Handle processes a voice speaking event from Discord.
func (h *VoiceSpeakingHandler) Handle(vc *discordgo.VoiceConnection, vsu *discordgo.VoiceSpeakingUpdate) {
	session, exists := h.manager.GetSession(h.guildID)
	if !exists {
		return
	}

	logger.Debug("Voice speaking event",
		zap.String("guild_id", h.guildID),
		zap.String("user_id", vsu.UserID),
		zap.Bool("speaking", vsu.Speaking))

	if vsu.Speaking {
		// User started speaking
		session.CurrentSpeaker = vsu.UserID
		h.manager.SetState(h.guildID, StateListening)
	} else {
		// User stopped speaking
		session.CurrentSpeaker = ""
	}
}
