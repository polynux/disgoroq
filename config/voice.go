package config

import "time"

// VoiceConfig contains voice chat configuration.
type VoiceConfig struct {
	Enabled bool      `yaml:"enabled"`
	TTS     TTSConfig `yaml:"tts"`
	STT     STTConfig `yaml:"stt"`
	VRAM    VRAMConfig `yaml:"vram"`
	Audio   AudioConfig `yaml:"audio"`
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
}

// STTConfig contains speech-to-text configuration.
type STTConfig struct {
	SocketPath string `yaml:"socket_path"`
	Model      string `yaml:"model"`
	Language   string `yaml:"language"`
}

// VRAMConfig contains GPU memory management configuration.
type VRAMConfig struct {
	MinFreeMB          int `yaml:"min_free_mb"`
	AutoUnload         bool `yaml:"auto_unload"`
	UnloadTimeoutSeconds int `yaml:"unload_timeout_seconds"`
}

// AudioConfig contains audio processing configuration.
type AudioConfig struct {
	FrameSize   int `yaml:"frame_size"`    // Samples per frame (20ms at 48kHz = 960)
	SampleRate  int `yaml:"sample_rate"`   // Discord uses 48kHz
	Channels    int `yaml:"channels"`      // Stereo = 2
	BufferMs    int `yaml:"buffer_ms"`     // Audio buffer before transcription
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
			MinFreeMB:          1500,
			AutoUnload:         true,
			UnloadTimeoutSeconds: 60,
		},
		Audio: AudioConfig{
			FrameSize:  960,   // 20ms at 48kHz
			SampleRate: 48000, // Discord uses 48kHz
			Channels:   2,     // Stereo
			BufferMs:   500,   // 500ms buffer before transcription
		},
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