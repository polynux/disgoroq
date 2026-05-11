// Package config provides centralized configuration management for DisgoroQ.
// Configuration is loaded from a YAML file with environment variable interpolation support.
package config

import "time"

// Config is the root configuration structure containing all nested configurations.
type Config struct {
	Discord   DiscordConfig   `yaml:"discord"`
	Database  DatabaseConfig  `yaml:"database"`
	AI        AIConfig        `yaml:"ai"`
	Logging   LoggingConfig   `yaml:"logging"`
	Memory    MemoryConfig    `yaml:"memory"`
	Emoji     EmojiConfig     `yaml:"emoji"`
	Horoscope HoroscopeConfig `yaml:"horoscope"`
	Reengage  ReengageConfig  `yaml:"reengage"`
	Bot       BotConfig       `yaml:"bot"`
	Voice     VoiceConfig     `yaml:"voice"`
}

// DiscordConfig contains Discord-related configuration.
type DiscordConfig struct {
	Token       string   `yaml:"token"`
	DevGuildIDs []string `yaml:"dev_guild_ids"`
}

// BotConfig contains bot personality configuration.
type BotConfig struct {
	DefaultPrompt string   `yaml:"default_prompt"`
	TriggerWords  []string `yaml:"trigger_words"`
}

// DatabaseConfig contains database-related configuration.
type DatabaseConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
	Local bool   `yaml:"local"`
}

// AIConfig contains AI service configuration.
type AIConfig struct {
	PrimaryProvider   string           `yaml:"primary_provider"`
	Groq              GroqConfig       `yaml:"groq"`
	Ollama            OllamaConfig     `yaml:"ollama"`
	Opencode          OpencodeConfig   `yaml:"opencode"`
	Openrouter        OpenrouterConfig `yaml:"openrouter"`
	Retry             RetryConfig      `yaml:"retry"`
	FallbackEnabled   bool             `yaml:"fallback_enabled"`
	MinResponseLength int              `yaml:"min_response_length"`
}

const (
	AIProviderGroq       = "groq"
	AIProviderOllama     = "ollama"
	AIProviderOpencode   = "opencode"
	AIProviderOpenrouter = "openrouter"
)

// GroqConfig contains configuration for the Groq AI provider.
type GroqConfig struct {
	APIKey          string `yaml:"api_key"`
	Model           string `yaml:"model"`
	VisionModel     string `yaml:"vision_model"`
	ThinkingEnabled bool   `yaml:"thinking_enabled"`
}

// OllamaConfig contains configuration for the Ollama AI provider.
type OllamaConfig struct {
	Enabled         bool   `yaml:"enabled"`
	URL             string `yaml:"url"`
	Model           string `yaml:"model"`
	VisionModel     string `yaml:"vision_model"`
	ThinkingEnabled bool   `yaml:"thinking_enabled"`
}

// OpencodeConfig contains configuration for the OpenCode Go AI provider.
type OpencodeConfig struct {
	Enabled         bool   `yaml:"enabled"`
	BaseURL         string `yaml:"base_url"`
	APIKey          string `yaml:"api_key"`
	Model           string `yaml:"model"`
	VisionModel     string `yaml:"vision_model"`
	ThinkingEnabled bool   `yaml:"thinking_enabled"`
}

// OpenrouterConfig contains configuration for the OpenRouter AI provider.
type OpenrouterConfig struct {
	Enabled         bool   `yaml:"enabled"`
	BaseURL         string `yaml:"base_url"`
	APIKey          string `yaml:"api_key"`
	Model           string `yaml:"model"`
	VisionModel     string `yaml:"vision_model"`
	ThinkingEnabled bool   `yaml:"thinking_enabled"`
}

// RetryConfig contains retry logic configuration.
// Note: InitialDelay and MaxDelay are stored as durations internally but parsed from
// milliseconds in YAML (initial_delay_ms, max_delay_ms).
type RetryConfig struct {
	MaxRetries    int           `yaml:"max_retries"`
	InitialDelay  time.Duration `yaml:"-"` // Set from InitialDelayMs after parsing
	MaxDelay      time.Duration `yaml:"-"` // Set from MaxDelayMs after parsing
	BackoffFactor float64       `yaml:"backoff_factor"`
	RetryOnEmpty  bool          `yaml:"retry_on_empty"`
	RetryOnError  bool          `yaml:"retry_on_error"`

	// YAML fields for duration values (milliseconds)
	InitialDelayMs int `yaml:"initial_delay_ms"`
	MaxDelayMs     int `yaml:"max_delay_ms"`
}

// LoggingConfig contains logging-related configuration.
type LoggingConfig struct {
	Enabled             bool   `yaml:"enabled"`
	LogToDB             bool   `yaml:"log_to_db"`
	EventLoggingEnabled bool   `yaml:"event_logging_enabled"`
	Level               string `yaml:"level"`
	Encoding            string `yaml:"encoding"`
	RetentionDays       int    `yaml:"retention_days"`
	DBLogLevel          string `yaml:"db_log_level"`
}

// MemoryConfig contains memory service configuration.
// Note: SummaryInterval is stored as a duration internally but parsed from
// seconds in YAML (summary_interval_seconds).
type MemoryConfig struct {
	Enabled            bool          `yaml:"enabled"`
	OllamaURL          string        `yaml:"ollama_url"`
	EmbeddingModel     string        `yaml:"embedding_model"`
	SummaryModel       string        `yaml:"summary_model"`
	BufferThreshold    int           `yaml:"buffer_threshold"`
	SummaryInterval    time.Duration `yaml:"-"` // Set from SummaryIntervalSeconds after parsing
	MaxContextMessages int           `yaml:"max_context_messages"`
	MaxSummaryContext  int           `yaml:"max_summary_context"`

	// YAML field for duration value (seconds)
	SummaryIntervalSeconds int `yaml:"summary_interval_seconds"`
}

// EmojiConfig contains emoji manager configuration.
type EmojiConfig struct {
	CacheTTLMinutes int `yaml:"cache_ttl_minutes"`
}

// HoroscopeConfig contains horoscope scheduler configuration.
type HoroscopeConfig struct {
	IncludeEmojis bool `yaml:"include_emojis"`
}

// ReengageConfig contains reengagement feature configuration.
type ReengageConfig struct {
	CheckIntervalSeconds     int     `yaml:"check_interval_seconds"`
	DefaultInactivityMinutes int     `yaml:"default_inactivity_minutes"`
	DefaultChance            float64 `yaml:"default_chance"`
	ReengageMessage          string  `yaml:"reengage_message"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Discord: DiscordConfig{
			Token:       "",
			DevGuildIDs: nil,
		},
		Database: DatabaseConfig{
			URL:   "",
			Token: "",
			Local: false,
		},
		AI: AIConfig{
			PrimaryProvider: AIProviderGroq,
			Groq: GroqConfig{
				Model:           "openai/gpt-oss-20b",
				VisionModel:     "meta-llama/llama-4-scout-17b-16e-instruct",
				ThinkingEnabled: true,
			},
			Ollama: OllamaConfig{
				Enabled:         false,
				URL:             "http://localhost:11434",
				Model:           "dolphin3",
				VisionModel:     "llava",
				ThinkingEnabled: false,
			},
			Opencode: OpencodeConfig{
				Enabled:         false,
				BaseURL:         "https://opencode.ai/zen/go/v1",
				Model:           "deepseek-v4-flash",
				VisionModel:     "deepseek-v4-flash",
				ThinkingEnabled: true,
			},
			Openrouter: OpenrouterConfig{
				Enabled:         false,
				BaseURL:         "https://openrouter.ai/api/v1",
				Model:           "google/gemini-2.5-flash",
				VisionModel:     "google/gemini-2.5-flash",
				ThinkingEnabled: true,
			},
			Retry: RetryConfig{
				MaxRetries:     2,
				InitialDelay:   500 * time.Millisecond,
				MaxDelay:       5 * time.Second,
				BackoffFactor:  2.0,
				RetryOnEmpty:   true,
				RetryOnError:   true,
				InitialDelayMs: 500,
				MaxDelayMs:     5000,
			},
			FallbackEnabled:   true,
			MinResponseLength: 1,
		},
		Logging: LoggingConfig{
			Enabled:             true,
			LogToDB:             false,
			EventLoggingEnabled: true,
			Level:               "info",
			Encoding:            "json",
			RetentionDays:       7,
			DBLogLevel:          "info",
		},
		Memory: MemoryConfig{
			Enabled:                true,
			OllamaURL:              "http://localhost:11434",
			EmbeddingModel:         "nomic-embed-text",
			SummaryModel:           "llama3-8b-8192",
			BufferThreshold:        10,
			SummaryInterval:        1 * time.Hour,
			MaxContextMessages:     5,
			MaxSummaryContext:      3,
			SummaryIntervalSeconds: 3600, // 1 hour
		},
		Emoji: EmojiConfig{
			CacheTTLMinutes: 60,
		},
		Horoscope: HoroscopeConfig{
			IncludeEmojis: true,
		},
		Reengage: ReengageConfig{
			CheckIntervalSeconds:     300,
			DefaultInactivityMinutes: 30,
			DefaultChance:            0.01,
			ReengageMessage:          "\n\nLe chat est calme, relance la discussion avec quelque chose de fun! Pose une question, raconte une anecdote, fais une vanne ou lance un sujet de discussion. Reste dans ton personnage!",
		},
		Bot: BotConfig{
			DefaultPrompt: "yo, t'es {{.BotNick}}, un pur bg du brainrot, élevé à la sauce tiktok, 10 écrans en simultané, et t'envoies du lourd ! 🔥 pas de majuscules, jamais, t'es un pote déjanté qui balance des vannes non-stop, avec du slang gen z, des refs pop culture et des vibes gaming. si on te pose une question, tu réponds direct, sinon tu surfes sur la vibe du dernier message, toujours ultra court (2-3 phrases max), sans sortir du délire, même sur du sérieux. pas d'ia, pas d'assistant, juste un bro qui claque des émojis et du fun ! 🚀",
			TriggerWords:  []string{"feun", "feunboy"},
		},
		Voice: VoiceConfig{
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
				FrameSize:  960,
				SampleRate: 48000,
				Channels:   2,
				BufferMs:   0, // Deprecated
				// VAD settings
				VADSilenceMs:          700,   // 700ms silence = user stopped talking
				VADSpeechMinMs:        300,   // Minimum 300ms speech to process
				VADMaxDurationMs:      10000, // 10 seconds max recording
				VADAmplitudeThreshold: 0.02,  // 2% of max amplitude
				// Streaming settings
				StreamBufferSize: 200, // Pre-buffer 200ms before playing
			},
		},
	}
}
