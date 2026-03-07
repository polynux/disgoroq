package config

import "time"

// VoiceConfig contains voice chat configuration.
type VoiceConfig struct {
	Enabled               bool        `yaml:"enabled"`
	TTS                   TTSConfig   `yaml:"tts"`
	STT                   STTConfig   `yaml:"stt"`
	VRAM                  VRAMConfig  `yaml:"vram"`
	Audio                 AudioConfig `yaml:"audio"`
	IdleTimeoutMs         int         `yaml:"idle_timeout_ms"`          // Idle timeout between AI speech and next voice processing (ms)
	VoiceContextMaxLength int         `yaml:"voice_context_max_length"` // Maximum messages in voice conversation history
	VoiceSystemPrompt     string      `yaml:"voice_system_prompt"`      // Separate system prompt for voice mode
}

// TTSConfig contains TTS service configuration.
type TTSConfig struct {
	Endpoint       string `yaml:"endpoint"`
	Model          string `yaml:"model"`
	TimeoutMs      int    `yaml:"timeout_ms"`
	DefaultVoice   string `yaml:"default_voice"`
	SampleRate     int    `yaml:"sample_rate"`
	StaticMode     bool   `yaml:"static_mode"`      // Use static generation (generate entire response before playing)
	MaxTextLength  int    `yaml:"max_text_length"`  // Maximum text length per TTS request
	FallbackToText bool   `yaml:"fallback_to_text"` // Send text message if TTS fails
	AlwaysSendText bool   `yaml:"always_send_text"` // Always send text response in chat (in addition to TTS)
}

// STTConfig contains speech-to-text configuration.
type STTConfig struct {
	SocketPath string `yaml:"socket_path"`
	Model      string `yaml:"model"`
	Language   string `yaml:"language"`
}

// VRAMConfig contains GPU memory management configuration.
type VRAMConfig struct {
	MinFreeMB            int  `yaml:"min_free_mb"`
	AutoUnload           bool `yaml:"auto_unload"`
	UnloadTimeoutSeconds int  `yaml:"unload_timeout_seconds"`
}

// AudioConfig contains audio processing configuration.
type AudioConfig struct {
	FrameSize  int `yaml:"frame_size"`  // Samples per frame (20ms at 48kHz = 960)
	SampleRate int `yaml:"sample_rate"` // Discord uses 48kHz
	Channels   int `yaml:"channels"`    // Stereo = 2
	BufferMs   int `yaml:"buffer_ms"`   // Deprecated: use VAD settings instead
	// VAD (Voice Activity Detection) settings
	VADSilenceMs          int     `yaml:"vad_silence_ms"`          // Silence duration to trigger transcription (ms)
	VADSpeechMinMs        int     `yaml:"vad_speech_min_ms"`       // Minimum speech duration to process (ms)
	VADMaxDurationMs      int     `yaml:"vad_max_duration_ms"`     // Maximum recording duration (ms)
	VADAmplitudeThreshold float64 `yaml:"vad_amplitude_threshold"` // Amplitude threshold (0.0-1.0)
	// Streaming playback settings
	StreamBufferSize int `yaml:"stream_buffer_size"` // Pre-buffer size for streaming (ms)
}

// GetVoiceConfigDefaults returns the default voice configuration.
func GetVoiceConfigDefaults() VoiceConfig {
	return VoiceConfig{
		Enabled: false,
		TTS: TTSConfig{
			Endpoint:       "http://localhost:8880",
			Model:          "Qwen/Qwen3-TTS-12Hz-0.6B-Base",
			TimeoutMs:      30000,
			DefaultVoice:   "default",
			SampleRate:     24000,
			StaticMode:     true,
			MaxTextLength:  500,
			FallbackToText: true,
		},
		STT: STTConfig{
			SocketPath: "/tmp/whisper.sock",
			Model:      "tiny",
			Language:   "fr",
		},
		VRAM: VRAMConfig{
			MinFreeMB:            1500,
			AutoUnload:           true,
			UnloadTimeoutSeconds: 60,
		},
		Audio: AudioConfig{
			FrameSize:  960,   // 20ms at 48kHz
			SampleRate: 48000, // Discord uses 48kHz
			Channels:   2,     // Stereo
			BufferMs:   0,     // Deprecated: VAD handles this now
			// VAD settings - trigger transcription after silence
			VADSilenceMs:          700,   // 700ms silence = user stopped talking
			VADSpeechMinMs:        300,   // Minimum 300ms of speech to process
			VADMaxDurationMs:      10000, // 10 seconds max recording
			VADAmplitudeThreshold: 0.02,  // Voice activity threshold
			// Streaming settings
			StreamBufferSize: 200, // Pre-buffer 200ms before playing
		},
		IdleTimeoutMs:         3000, // 3 seconds between AI speech and next voice processing
		VoiceContextMaxLength: 10,   // Keep last 10 messages in voice conversation history
		VoiceSystemPrompt:     "Tu es en conversation vocale. Réponds de manière concise et naturelle, comme dans une vraie conversation. Évite les réponses trop longues.",
	}
}

// TTSConfigWithDurations returns TTS config with timeout as duration.
func (c TTSConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutMs) * time.Millisecond
}

// VRAMConfigWithDurations returns VRAM config with timeout as duration.
func (c VRAMConfig) UnloadTimeout() time.Duration {
	return time.Duration(c.UnloadTimeoutSeconds) * time.Second
}

// AudioConfigDuration returns buffer duration.
func (c AudioConfig) BufferDuration() time.Duration {
	return time.Duration(c.BufferMs) * time.Millisecond
}

// FrameDuration returns the duration of a single audio frame.
func (c AudioConfig) FrameDuration() time.Duration {
	// Frame size / sample rate = duration in seconds
	// e.g., 960 / 48000 = 0.02 seconds = 20ms
	return time.Duration(c.FrameSize*1000/c.SampleRate) * time.Millisecond
}

// IdleTimeout returns the idle timeout as a duration.
func (c VoiceConfig) IdleTimeout() time.Duration {
	return time.Duration(c.IdleTimeoutMs) * time.Millisecond
}
