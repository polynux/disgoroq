// Package voice provides voice chat capabilities for the Discord bot.
// It handles voice connections, speech-to-text, and text-to-speech integration.
package voice

import (
	"context"
	"errors"
	"io"
)

// AgentState represents the current state of the voice agent.
type AgentState int

const (
	// StateIdle means the bot is not in a voice channel or is idle.
	StateIdle AgentState = iota
	// StateListening means the bot is actively listening to user input.
	StateListening
	// StateThinking means the bot is processing the user's input with AI.
	StateThinking
	// StateSpeaking means the bot is playing TTS audio back to the channel.
	StateSpeaking
)

// String returns a human-readable representation of the state.
func (s AgentState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateListening:
		return "listening"
	case StateThinking:
		return "thinking"
	case StateSpeaking:
		return "speaking"
	default:
		return "unknown"
	}
}

// Common errors for the voice package.
var (
	ErrNotConnected      = errors.New("not connected to voice channel")
	ErrAlreadyConnected  = errors.New("already connected to a voice channel")
	ErrStateTransition   = errors.New("invalid state transition")
	ErrSTTUnavailable    = errors.New("speech-to-text service unavailable")
	ErrTTSUnavailable    = errors.New("text-to-speech service unavailable")
	ErrInsufficientVRAM  = errors.New("insufficient VRAM for TTS model")
	ErrTranscriptionFail = errors.New("transcription failed")
	ErrTTSError          = errors.New("TTS generation failed")
	ErrAudioBufferEmpty  = errors.New("audio buffer is empty")
	ErrContextCanceled   = errors.New("context canceled")
)

// STTClient defines the interface for speech-to-text clients.
type STTClient interface {
	// Transcribe sends audio data for transcription and returns the text.
	Transcribe(ctx context.Context, audio []byte) (string, error)

	// StartStreaming starts a streaming transcription session.
	StartStreaming(ctx context.Context) error

	// StopStreaming stops the streaming session and returns any remaining transcription.
	StopStreaming() (string, error)

	// SendAudio sends audio data to an active streaming session.
	SendAudio(audio []byte) error

	// IsConnected returns whether the client is connected to the STT service.
	IsConnected() bool

	// Close closes the connection to the STT service.
	Close() error
}

// StreamChunk represents a chunk of streaming audio from the TTS service.
type StreamChunk struct {
	// Data is the raw PCM audio bytes.
	Data []byte
	// SampleRate is the sample rate of the audio.
	SampleRate int
	// Format is the audio format (pcm, wav, etc.).
	Format string
	// Err contains any error that occurred during streaming.
	Err error
}

// TTSClient defines the interface for text-to-speech clients.
type TTSClient interface {
	// Stream generates audio from text and returns a stream of audio data.
	Stream(ctx context.Context, req *TTSRequest) (io.ReadCloser, error)

	// StreamStreaming generates audio with real-time streaming chunks.
	// Returns a channel that yields audio chunks as they arrive from the server.
	// This is significantly faster than waiting for complete generation because
	// the first audio starts playing within ~400-800ms instead of waiting for
	// the entire response to be generated.
	StreamStreaming(ctx context.Context, req *TTSRequest) (<-chan StreamChunk, error)

	// Generate generates complete audio for text and returns it as a byte slice.
	// This is the recommended method for static (non-streaming) TTS.
	Generate(ctx context.Context, req *TTSRequest) ([]byte, error)

	// LoadModel loads the TTS model into VRAM.
	LoadModel(ctx context.Context) error

	// UnloadModel unloads the TTS model from VRAM.
	UnloadModel(ctx context.Context) error

	// GetVRAM returns the available VRAM in MB.
	GetVRAM(ctx context.Context) (int64, error)

	// IsModelLoaded returns whether the TTS model is currently loaded.
	IsModelLoaded() bool

	// Close closes the connection to the TTS service.
	Close() error
}

// TTSRequest represents a text-to-speech request.
type TTSRequest struct {
	// Text is the text to synthesize.
	Text string `json:"text"`

	// VoiceID is an optional voice identifier for voice selection.
	VoiceID string `json:"voice_id,omitempty"`

	// SpeakerRef is an optional path to a reference audio file for voice cloning.
	SpeakerRef string `json:"speaker_ref,omitempty"`

	// SpeakerText is the transcript of the reference audio (for voice cloning).
	SpeakerText string `json:"speaker_text,omitempty"`
}

// TTSResponse represents metadata about a TTS response.
type TTSResponse struct {
	// Duration is the duration of the generated audio in seconds.
	Duration float64 `json:"duration"`

	// SampleRate is the sample rate of the generated audio.
	SampleRate int `json:"sample_rate"`

	// VRAMUsed is the VRAM used by the model in MB.
	VRAMUsed int64 `json:"vram_used"`
}

// VRAMStatus represents the current GPU memory status.
type VRAMStatus struct {
	// TotalMB is the total VRAM in megabytes.
	TotalMB int64 `json:"total_mb"`

	// UsedMB is the used VRAM in megabytes.
	UsedMB int64 `json:"used_mb"`

	// FreeMB is the free VRAM in megabytes.
	FreeMB int64 `json:"free_mb"`

	// ModelLoaded indicates if the TTS model is loaded.
	ModelLoaded bool `json:"model_loaded"`
}

// VoiceSession represents an active voice session in a guild.
type VoiceSession struct {
	// GuildID is the Discord guild ID.
	GuildID string

	// ChannelID is the Discord voice channel ID.
	ChannelID string

	// TextChannelID is the text channel for fallback messages.
	TextChannelID string

	// State is the current state of the voice agent.
	State AgentState

	// UserID tracks who is currently speaking (for single-user mode).
	CurrentSpeaker string

	// AudioBuffer accumulates audio for transcription.
	AudioBuffer *AudioBuffer
}

// AudioBuffer manages buffered audio data for transcription.
type AudioBuffer struct {
	// Data contains the raw audio bytes.
	Data []byte

	// SampleRate is the sample rate of the audio.
	SampleRate int

	// Channels is the number of audio channels.
	Channels int

	// DurationMs is the total duration in milliseconds.
	DurationMs int
}

// Append adds audio data to the buffer.
func (b *AudioBuffer) Append(data []byte, durationMs int) {
	b.Data = append(b.Data, data...)
	b.DurationMs += durationMs
}

// Clear resets the buffer.
func (b *AudioBuffer) Clear() {
	b.Data = b.Data[:0]
	b.DurationMs = 0
}

// Len returns the length of the buffered data.
func (b *AudioBuffer) Len() int {
	return len(b.Data)
}

// IsEmpty returns true if the buffer has no data.
func (b *AudioBuffer) IsEmpty() bool {
	return len(b.Data) == 0
}

// NewAudioBuffer creates a new audio buffer with pre-allocated capacity.
func NewAudioBuffer(initialCapacity int, sampleRate, channels int) *AudioBuffer {
	return &AudioBuffer{
		Data:       make([]byte, 0, initialCapacity),
		SampleRate: sampleRate,
		Channels:   channels,
	}
}

// TranscriptionResult represents the result of a transcription.
type TranscriptionResult struct {
	// Text is the transcribed text.
	Text string

	// Confidence is the confidence score (0-1).
	Confidence float64

	// Language is the detected language.
	Language string

	// Duration is the duration of the transcribed audio in seconds.
	Duration float64

	// IsFinal indicates if this is the final result.
	IsFinal bool
}

// StateTransition represents a state change event.
type StateTransition struct {
	// From is the previous state.
	From AgentState

	// To is the new state.
	To AgentState

	// GuildID is the guild where the transition occurred.
	GuildID string

	// Reason is an optional reason for the transition.
	Reason string
}

// VoiceConfig contains the voice configuration from the main config.
type VoiceConfig struct {
	Enabled         bool
	TTSEndpoint     string
	TTSModel        string
	TTSTimeout      int
	STTSocket       string
	STTModel        string
	STTLanguage     string
	MinVRAM         int
	AutoUnload      bool
	AudioFrameSize  int
	AudioSampleRate int
	AudioChannels   int
	AudioBufferMs   int
}
