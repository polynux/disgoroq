package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefault(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Set required environment variables
	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	t.Setenv("DB_URL", "http://localhost:8080")
	t.Setenv("DB_TOKEN", "test-db-token")

	// Write a minimal config file
	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
  local: false

ai:
  groq:
    api_key: "${GROQ_API_KEY}"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Load the config
	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Verify values
	if config.Discord.Token != "test-discord-token" {
		t.Errorf("Discord.Token = %v, want test-discord-token", config.Discord.Token)
	}
	if config.AI.Groq.APIKey != "test-groq-key" {
		t.Errorf("AI.Groq.APIKey = %v, want test-groq-key", config.AI.Groq.APIKey)
	}
	if config.AI.Ollama.CompletionSpeaker != "§bot" {
		t.Errorf("AI.Ollama.CompletionSpeaker = %q, want §bot", config.AI.Ollama.CompletionSpeaker)
	}
	if config.AI.Ollama.CompletionPromptChars != 2400 {
		t.Errorf("AI.Ollama.CompletionPromptChars = %d, want 2400", config.AI.Ollama.CompletionPromptChars)
	}
	if config.AI.Ollama.CompletionBotLines != 4 {
		t.Errorf("AI.Ollama.CompletionBotLines = %d, want 4", config.AI.Ollama.CompletionBotLines)
	}
	if config.AI.Ollama.CompletionMediaTags != "v0" {
		t.Errorf("AI.Ollama.CompletionMediaTags = %q, want v0", config.AI.Ollama.CompletionMediaTags)
	}
}

func TestLoadOllamaCompletionMediaConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	t.Setenv("DB_URL", "http://localhost:8080")
	t.Setenv("DB_TOKEN", "test-db-token")
	content := `
discord:
  token: "${DISCORD_TOKEN}"
database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
ai:
  groq:
    api_key: "${GROQ_API_KEY}"
  ollama:
    enabled: true
    url: "http://localhost:11434"
    model: "dolphin3"
    vision_model: "llava"
    completion_bot_lines: 6
    completion_media_tags: "v1"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.AI.Ollama.CompletionBotLines != 6 {
		t.Errorf("AI.Ollama.CompletionBotLines = %d, want 6", config.AI.Ollama.CompletionBotLines)
	}
	if config.AI.Ollama.CompletionMediaTags != "v1" {
		t.Errorf("AI.Ollama.CompletionMediaTags = %q, want v1", config.AI.Ollama.CompletionMediaTags)
	}

	if err := os.WriteFile(configPath, []byte(strings.Replace(content, `completion_media_tags: "v1"`, `completion_media_tags: "v2"`, 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Error("Load() accepted completion_media_tags v2")
	}

	if err := os.WriteFile(configPath, []byte(strings.Replace(content, "completion_bot_lines: 6", "completion_bot_lines: 0", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Error("Load() accepted completion_bot_lines 0")
	}
}

func TestLoadOllamaCompletionSpeakerCanBeEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	t.Setenv("DB_URL", "http://localhost:8080")
	t.Setenv("DB_TOKEN", "test-db-token")
	content := `
discord:
  token: "${DISCORD_TOKEN}"
database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
ai:
  groq:
    api_key: "${GROQ_API_KEY}"
  ollama:
    completion_speaker: ""
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.AI.Ollama.CompletionSpeaker != "" {
		t.Errorf("explicit empty completion speaker was replaced with %q", config.AI.Ollama.CompletionSpeaker)
	}
	if config.AI.Ollama.CompletionPromptChars != 2400 {
		t.Errorf("AI.Ollama.CompletionPromptChars = %d, want 2400", config.AI.Ollama.CompletionPromptChars)
	}
}

func TestLoadWithOllamaPrimary(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("DB_URL", "http://localhost:8080")
	t.Setenv("DB_TOKEN", "test-db-token")

	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
  local: false

ai:
  primary_provider: "ollama"
  ollama:
    enabled: true
    url: "http://localhost:11434"
    model: "dolphin3"
    vision_model: "llava"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.AI.PrimaryProvider != AIProviderOllama {
		t.Errorf("AI.PrimaryProvider = %v, want %v", config.AI.PrimaryProvider, AIProviderOllama)
	}
	if config.AI.Groq.APIKey != "" {
		t.Errorf("AI.Groq.APIKey = %v, want empty", config.AI.Groq.APIKey)
	}
}

func TestLoadWithOpencodePrimary(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("DB_URL", "http://localhost:8080")
	t.Setenv("DB_TOKEN", "test-db-token")
	t.Setenv("OPENCODE_API_KEY", "test-opencode-key")

	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
  local: false

ai:
  primary_provider: "opencode"
  opencode:
    enabled: true
    api_key: "${OPENCODE_API_KEY}"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.AI.PrimaryProvider != AIProviderOpencode {
		t.Errorf("AI.PrimaryProvider = %v, want %v", config.AI.PrimaryProvider, AIProviderOpencode)
	}
	if config.AI.Opencode.APIKey != "test-opencode-key" {
		t.Errorf("AI.Opencode.APIKey = %v, want test-opencode-key", config.AI.Opencode.APIKey)
	}
	if config.AI.Opencode.BaseURL != "https://opencode.ai/inference/go/v1" {
		t.Errorf("AI.Opencode.BaseURL = %q, want https://opencode.ai/inference/go/v1", config.AI.Opencode.BaseURL)
	}
	if config.AI.Opencode.Model != "deepseek-v4.1-flash" {
		t.Errorf("AI.Opencode.Model = %q, want deepseek-v4.1-flash", config.AI.Opencode.Model)
	}
	if config.AI.Opencode.VisionModel != "deepseek-v4.1-flash" {
		t.Errorf("AI.Opencode.VisionModel = %q, want deepseek-v4.1-flash", config.AI.Opencode.VisionModel)
	}
}

func TestLoadOpencodeCustomBaseURL(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("OPENCODE_API_KEY", "test-opencode-key")

	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  local: true

ai:
  primary_provider: "opencode"
  opencode:
    enabled: true
    base_url: "https://custom.opencode.example/v1"
    api_key: "${OPENCODE_API_KEY}"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.AI.Opencode.BaseURL != "https://custom.opencode.example/v1" {
		t.Errorf("AI.Opencode.BaseURL = %q, want https://custom.opencode.example/v1", config.AI.Opencode.BaseURL)
	}
}

func TestLoadWithOpenrouterPrimary(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("DB_URL", "http://localhost:8080")
	t.Setenv("DB_TOKEN", "test-db-token")
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")

	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
  local: false

ai:
  primary_provider: "openrouter"
  openrouter:
    enabled: true
    base_url: "https://openrouter.ai/api/v1"
    api_key: "${OPENROUTER_API_KEY}"
    model: "google/gemini-2.5-flash"
    vision_model: "google/gemini-2.5-flash"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.AI.PrimaryProvider != AIProviderOpenrouter {
		t.Errorf("AI.PrimaryProvider = %v, want %v", config.AI.PrimaryProvider, AIProviderOpenrouter)
	}
	if config.AI.Openrouter.APIKey != "test-openrouter-key" {
		t.Errorf("AI.Openrouter.APIKey = %v, want test-openrouter-key", config.AI.Openrouter.APIKey)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("Load() expected error for missing file, got nil")
	}
}

func TestLoadMissingEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write config with missing env var
	configContent := `
discord:
  token: "${MISSING_DISCORD_TOKEN}"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("Load() expected error for missing required env var, got nil")
	}
}

func TestEnvVarInterpolation(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		envVars  map[string]string
		expected string
		wantErr  bool
	}{
		{
			name:     "simple variable",
			input:    "${TEST_VAR}",
			envVars:  map[string]string{"TEST_VAR": "value"},
			expected: "value",
			wantErr:  false,
		},
		{
			name:     "variable with default",
			input:    "${TEST_VAR:-default}",
			envVars:  map[string]string{"TEST_VAR": "value"},
			expected: "value",
			wantErr:  false,
		},
		{
			name:     "missing variable with default",
			input:    "${MISSING_VAR:-default}",
			envVars:  map[string]string{},
			expected: "default",
			wantErr:  false,
		},
		{
			name:     "missing required variable",
			input:    "${MISSING_VAR}",
			envVars:  map[string]string{},
			expected: "",
			wantErr:  true,
		},
		{
			name:     "multiple variables",
			input:    "prefix_${VAR1}_middle_${VAR2}_suffix",
			envVars:  map[string]string{"VAR1": "one", "VAR2": "two"},
			expected: "prefix_one_middle_two_suffix",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			result, err := interpolateEnvVars([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("interpolateEnvVars() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && string(result) != tt.expected {
				t.Errorf("interpolateEnvVars() = %v, want %v", string(result), tt.expected)
			}
		})
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: &Config{
				Discord:  DiscordConfig{Token: "test-token", DevGuildIDs: []string{"123456789012345678"}},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test-token", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderGroq,
					Groq:            GroqConfig{APIKey: "test-key", Model: "model", VisionModel: "vision"},
					Ollama:          OllamaConfig{Enabled: false},
					Retry: RetryConfig{
						MaxRetries:     2,
						InitialDelay:   500 * time.Millisecond,
						MaxDelay:       5 * time.Second,
						BackoffFactor:  2.0,
						InitialDelayMs: 500,
						MaxDelayMs:     5000,
					},
					MinResponseLength: 1,
				},
				Logging: LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory: MemoryConfig{
					OllamaURL:              "http://localhost",
					EmbeddingModel:         "model",
					SummaryProvider:        SummaryProviderOllama,
					SummaryModel:           "model",
					BufferThreshold:        10,
					SummaryInterval:        1 * time.Hour,
					SummaryIntervalSeconds: 3600,
					MaxSummaryContext:      3,
				},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
				Voice:     VoiceConfig{},
			},
			wantErr: false,
		},
		{
			name: "missing discord token",
			config: &Config{
				Discord: DiscordConfig{Token: ""},
			},
			wantErr: true,
			errMsg:  "discord.token is required",
		},
		{
			name: "missing database URL when not local",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "", Token: "", Local: false},
			},
			wantErr: true,
			errMsg:  "database.url is required",
		},
		{
			name: "missing groq api key",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderGroq,
					Groq:            GroqConfig{APIKey: ""},
				},
			},
			wantErr: true,
			errMsg:  "ai.groq.api_key is required when ai.primary_provider is \"groq\"",
		},
		{
			name: "ollama primary without groq api key",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderOllama,
					Ollama:          OllamaConfig{Enabled: true, URL: "http://localhost:11434", Model: "dolphin3", VisionModel: "llava", CompletionBotLines: 4},
					Retry: RetryConfig{
						InitialDelay:  time.Millisecond,
						MaxDelay:      2 * time.Millisecond,
						BackoffFactor: 2.0,
					},
					MinResponseLength: 1,
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: false,
		},
		{
			name: "opencode primary config",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderOpencode,
					Opencode: OpencodeConfig{
						Enabled:     true,
						BaseURL:     "https://opencode.ai/inference/go/v1",
						APIKey:      "test-key",
						Model:       "deepseek-v4.1-flash",
						VisionModel: "deepseek-v4.1-flash",
					},
					Retry: RetryConfig{
						InitialDelay:  time.Millisecond,
						MaxDelay:      2 * time.Millisecond,
						BackoffFactor: 2.0,
					},
					MinResponseLength: 1,
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: false,
		},
		{
			name: "openrouter primary config",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderOpenrouter,
					Openrouter: OpenrouterConfig{
						Enabled:     true,
						BaseURL:     "https://openrouter.ai/api/v1",
						APIKey:      "test-key",
						Model:       "google/gemini-2.5-flash",
						VisionModel: "google/gemini-2.5-flash",
					},
					Retry: RetryConfig{
						InitialDelay:  time.Millisecond,
						MaxDelay:      2 * time.Millisecond,
						BackoffFactor: 2.0,
					},
					MinResponseLength: 1,
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: false,
		},
		{
			name: "invalid ai primary provider",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: "invalid",
					Retry: RetryConfig{
						InitialDelay:  time.Millisecond,
						MaxDelay:      2 * time.Millisecond,
						BackoffFactor: 2.0,
					},
					MinResponseLength: 1,
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: true,
			errMsg:  "ai.primary_provider must be one of",
		},
		{
			name: "invalid log level",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{Local: true},
				AI: AIConfig{
					Groq:  GroqConfig{APIKey: "test", Model: "model", VisionModel: "vision"},
					Retry: RetryConfig{InitialDelay: 1 * time.Millisecond, MaxDelay: 2 * time.Millisecond, BackoffFactor: 2.0},
				},
				Logging: LoggingConfig{Level: "invalid", Encoding: "json", DBLogLevel: "info"},
			},
			wantErr: true,
			errMsg:  "logging.level must be one of",
		},
		{
			name: "invalid dev guild ids",
			config: &Config{
				Discord:  DiscordConfig{Token: "test", DevGuildIDs: []string{""}},
				Database: DatabaseConfig{Local: true},
				AI: AIConfig{
					Groq:  GroqConfig{APIKey: "test", Model: "model", VisionModel: "vision"},
					Retry: RetryConfig{InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, BackoffFactor: 2.0},
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: true,
			errMsg:  "discord.dev_guild_ids cannot contain empty values",
		},
		{
			name: "invalid voice amplitude threshold",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{Local: true},
				AI: AIConfig{
					Groq:  GroqConfig{APIKey: "test", Model: "model", VisionModel: "vision"},
					Retry: RetryConfig{InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, BackoffFactor: 2.0},
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
				Voice: VoiceConfig{
					Enabled: true,
					TTS:     TTSConfig{Endpoint: "http://localhost:8880", TimeoutMs: 1000},
					STT:     STTConfig{SocketPath: "/tmp/whisper.sock"},
					Audio:   AudioConfig{SampleRate: 48000, Channels: 2, VADAmplitudeThreshold: 1.5},
				},
			},
			wantErr: true,
			errMsg:  "voice.audio.vad_amplitude_threshold must be between 0 and 1",
		},
		{
			name: "invalid tool loop config",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderGroq,
					Groq:            GroqConfig{APIKey: "test-key", Model: "model", VisionModel: "vision"},
					Tools: ToolsConfig{
						Enabled:          true,
						MaxRounds:        0,
						MaxCallsPerRound: 1,
						MaxCallsTotal:    1,
						Timeout:          time.Second,
						Web: WebFetchToolConfig{
							Enabled:        true,
							AllowedSchemes: []string{"http", "https"},
							UserAgent:      "test-agent",
							MaxBytes:       1024,
							MaxCharacters:  1024,
						},
					},
					Retry: RetryConfig{
						InitialDelay:  time.Millisecond,
						MaxDelay:      2 * time.Millisecond,
						BackoffFactor: 2.0,
					},
					MinResponseLength: 1,
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: true,
			errMsg:  "ai.tools.max_rounds must be at least 1",
		},
		{
			name: "invalid search config",
			config: &Config{
				Discord:  DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					PrimaryProvider: AIProviderGroq,
					Groq:            GroqConfig{APIKey: "test-key", Model: "model", VisionModel: "vision"},
					Tools: ToolsConfig{
						Enabled:          true,
						MaxRounds:        1,
						MaxCallsPerRound: 1,
						MaxCallsTotal:    1,
						Timeout:          time.Second,
						Web: WebFetchToolConfig{
							Enabled:        true,
							AllowedSchemes: []string{"http", "https"},
							UserAgent:      "test-agent",
							MaxBytes:       1024,
							MaxCharacters:  1024,
						},
						Search: SearchToolConfig{
							Enabled:       true,
							Provider:      "unknown",
							BaseURL:       "http://localhost:8081",
							UserAgent:     "test-agent",
							MaxResults:    5,
							MaxCharacters: 1024,
							SafeSearch:    1,
						},
					},
					Retry: RetryConfig{
						InitialDelay:  time.Millisecond,
						MaxDelay:      2 * time.Millisecond,
						BackoffFactor: 2.0,
					},
					MinResponseLength: 1,
				},
				Logging:   LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:    MemoryConfig{Enabled: false},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
				Reengage:  ReengageConfig{CheckIntervalSeconds: 300, DefaultInactivityMinutes: 30, DefaultChance: 0.1},
				Bot:       BotConfig{DefaultPrompt: "hello", TriggerWords: []string{"feun", "feunboy"}},
			},
			wantErr: true,
			errMsg:  "ai.tools.search.provider only supports searxng",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.Votes == (VotesConfig{}) {
				tt.config.Votes = DefaultConfig().Votes
			}
			if tt.config.MediaLibrary == (MediaLibraryConfig{}) {
				tt.config.MediaLibrary = DefaultConfig().MediaLibrary
			}
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !containsString(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestMemorySummaryProviderValidation(t *testing.T) {
	validAI := AIConfig{
		PrimaryProvider: AIProviderGroq,
		Groq:            GroqConfig{APIKey: "test-key", Model: "model", VisionModel: "vision"},
		Ollama:          OllamaConfig{Enabled: false},
		Retry: RetryConfig{
			MaxRetries:     2,
			InitialDelay:   500 * time.Millisecond,
			MaxDelay:       5 * time.Second,
			BackoffFactor:  2.0,
			InitialDelayMs: 500,
			MaxDelayMs:     5000,
		},
		MinResponseLength: 1,
	}
	validMemory := func(provider string) MemoryConfig {
		return MemoryConfig{
			Enabled:                true,
			OllamaURL:              "http://localhost:11434",
			EmbeddingModel:         "nomic-embed-text",
			SummaryProvider:        provider,
			SummaryModel:           "model",
			BufferThreshold:        10,
			SummaryInterval:        1 * time.Hour,
			SummaryIntervalSeconds: 3600,
			MaxSummaryContext:      3,
		}
	}

	tests := []struct {
		name     string
		memory   MemoryConfig
		ai       AIConfig
		wantErr  bool
		errParts []string
	}{
		{
			name:    "ollama provider valid",
			memory:  validMemory(SummaryProviderOllama),
			ai:      validAI,
			wantErr: false,
		},
		{
			name:    "groq provider with ai key",
			memory:  validMemory(SummaryProviderGroq),
			ai:      validAI,
			wantErr: false,
		},
		{
			name:     "groq provider without any key",
			memory:   validMemory(SummaryProviderGroq),
			ai:       AIConfig{PrimaryProvider: AIProviderGroq, Ollama: OllamaConfig{Enabled: false}},
			wantErr:  true,
			errParts: []string{"memory.summary_api_key"},
		},
		{
			name:   "opencode provider with ai.opencode.api_key only",
			memory: validMemory(SummaryProviderOpencode),
			ai: AIConfig{
				PrimaryProvider: AIProviderGroq,
				Opencode:        OpencodeConfig{APIKey: "opencode-ai-key", BaseURL: "https://opencode.ai/inference/go/v1"},
			},
			wantErr: false,
		},
		{
			name: "opencode provider with memory.summary_api_key only",
			memory: func() MemoryConfig {
				m := validMemory(SummaryProviderOpencode)
				m.SummaryAPIKey = "opencode-summary-key"
				return m
			}(),
			ai:      AIConfig{PrimaryProvider: AIProviderGroq, Opencode: OpencodeConfig{BaseURL: "https://opencode.ai/inference/go/v1"}},
			wantErr: false,
		},
		{
			name:   "opencode provider with a key but an empty base_url",
			memory: validMemory(SummaryProviderOpencode),
			ai: AIConfig{
				PrimaryProvider: AIProviderGroq,
				Opencode:        OpencodeConfig{APIKey: "opencode-ai-key", BaseURL: "  "},
			},
			wantErr:  true,
			errParts: []string{"ai.opencode.base_url", "opencode"},
		},
		{
			name:     "opencode provider without any key",
			memory:   validMemory(SummaryProviderOpencode),
			ai:       AIConfig{PrimaryProvider: AIProviderGroq},
			wantErr:  true,
			errParts: []string{"memory.summary_api_key", "opencode", "ai.opencode.api_key"},
		},
		{
			name:     "unknown provider",
			memory:   validMemory("bananaprovider"),
			ai:       validAI,
			wantErr:  true,
			errParts: []string{"memory.summary_provider"},
		},
		{
			name:     "empty provider",
			memory:   validMemory(""),
			ai:       validAI,
			wantErr:  true,
			errParts: []string{"memory.summary_provider"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Discord:  DiscordConfig{Token: "test-token", DevGuildIDs: []string{"123456789012345678"}},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test-token", Local: false},
				AI:       tt.ai,
				Logging:  LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory:   tt.memory,
				Emoji:    EmojiConfig{CacheTTLMinutes: 60},
			}

			err := cfg.Memory.validate(&cfg.AI)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errParts != nil {
				for _, part := range tt.errParts {
					if !contains(err.Error(), part) {
						t.Errorf("validate() error %q does not contain %q", err.Error(), part)
					}
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestDurationConversion(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Set required environment variables
	t.Setenv("DISCORD_TOKEN", "test-token")
	t.Setenv("GROQ_API_KEY", "test-key")
	t.Setenv("DB_URL", "http://localhost")
	t.Setenv("DB_TOKEN", "test-token")

	// Write a config file with duration values
	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  url: "${DB_URL}"
  token: "${DB_TOKEN}"
  local: false

ai:
  groq:
    api_key: "${GROQ_API_KEY}"
    model: "test-model"
    vision_model: "test-vision"
  tools:
    enabled: true
    max_rounds: 4
    max_calls_per_round: 2
    max_calls_total: 5
    timeout_ms: 1500
    web:
      enabled: true
      allowed_schemes: ["http", "https"]
      user_agent: "test-agent"
      max_bytes: 4096
      max_characters: 2048
    search:
      enabled: true
      provider: "searxng"
      base_url: "http://localhost:8081"
      user_agent: "test-agent"
      max_results: 7
      max_characters: 4096
      default_language: "fr"
      safe_search: 2
  retry:
    max_retries: 3
    initial_delay_ms: 1000
    max_delay_ms: 10000
    backoff_factor: 1.5
    retry_on_empty: false
    retry_on_error: true
  fallback_enabled: false
  min_response_length: 5

memory:
  enabled: true
  ollama_url: "http://localhost:11434"
  embedding_model: "test-embed"
  summary_model: "test-summary"
  buffer_threshold: 15
  summary_interval_seconds: 1800
  max_context_messages: 10
  max_summary_context: 5
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Verify retry config
	if config.AI.Retry.MaxRetries != 3 {
		t.Errorf("MaxRetries = %v, want 3", config.AI.Retry.MaxRetries)
	}
	if config.AI.Retry.InitialDelay != 1*time.Second {
		t.Errorf("InitialDelay = %v, want 1s", config.AI.Retry.InitialDelay)
	}
	if config.AI.Retry.MaxDelay != 10*time.Second {
		t.Errorf("MaxDelay = %v, want 10s", config.AI.Retry.MaxDelay)
	}
	if config.AI.Retry.BackoffFactor != 1.5 {
		t.Errorf("BackoffFactor = %v, want 1.5", config.AI.Retry.BackoffFactor)
	}
	if config.AI.Retry.RetryOnEmpty != false {
		t.Errorf("RetryOnEmpty = %v, want false", config.AI.Retry.RetryOnEmpty)
	}
	if config.AI.Tools.Timeout != 1500*time.Millisecond {
		t.Errorf("Tools.Timeout = %v, want 1500ms", config.AI.Tools.Timeout)
	}
	if config.AI.Tools.MaxRounds != 4 {
		t.Errorf("Tools.MaxRounds = %v, want 4", config.AI.Tools.MaxRounds)
	}
	if config.AI.Tools.Web.MaxBytes != 4096 {
		t.Errorf("Tools.Web.MaxBytes = %v, want 4096", config.AI.Tools.Web.MaxBytes)
	}
	if config.AI.Tools.Search.MaxResults != 7 {
		t.Errorf("Tools.Search.MaxResults = %v, want 7", config.AI.Tools.Search.MaxResults)
	}
	if config.AI.Tools.Search.SafeSearch != 2 {
		t.Errorf("Tools.Search.SafeSearch = %v, want 2", config.AI.Tools.Search.SafeSearch)
	}

	// Verify memory config
	if config.Memory.SummaryInterval != 30*time.Minute {
		t.Errorf("SummaryInterval = %v, want 30m", config.Memory.SummaryInterval)
	}
	if config.Memory.BufferThreshold != 15 {
		t.Errorf("BufferThreshold = %v, want 15", config.Memory.BufferThreshold)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Discord.Token != "" {
		t.Error("Default Discord.Token should be empty")
	}
	if config.AI.Groq.Model != "openai/gpt-oss-20b" {
		t.Errorf("Default Groq.Model = %v, want openai/gpt-oss-20b", config.AI.Groq.Model)
	}
	if config.AI.PrimaryProvider != AIProviderGroq {
		t.Errorf("Default AI.PrimaryProvider = %v, want %v", config.AI.PrimaryProvider, AIProviderGroq)
	}
	if config.AI.VisionProvider != "" {
		t.Errorf("Default AI.VisionProvider = %q, want empty", config.AI.VisionProvider)
	}
	if len(config.AI.FallbackOrder) != 0 {
		t.Errorf("Default AI.FallbackOrder = %v, want empty", config.AI.FallbackOrder)
	}
	if !config.AI.Groq.ThinkingEnabled {
		t.Error("Default AI.Groq.ThinkingEnabled should be true")
	}
	if config.AI.Ollama.ThinkingEnabled {
		t.Error("Default AI.Ollama.ThinkingEnabled should be false")
	}
	if !config.AI.Opencode.ThinkingEnabled {
		t.Error("Default AI.Opencode.ThinkingEnabled should be true")
	}
	if config.AI.Opencode.BaseURL != "https://opencode.ai/inference/go/v1" {
		t.Errorf("Default AI.Opencode.BaseURL = %q, want https://opencode.ai/inference/go/v1", config.AI.Opencode.BaseURL)
	}
	if config.AI.Opencode.Model != "deepseek-v4.1-flash" {
		t.Errorf("Default AI.Opencode.Model = %q, want deepseek-v4.1-flash", config.AI.Opencode.Model)
	}
	if config.AI.Opencode.VisionModel != "deepseek-v4.1-flash" {
		t.Errorf("Default AI.Opencode.VisionModel = %q, want deepseek-v4.1-flash", config.AI.Opencode.VisionModel)
	}
	if !config.AI.Openrouter.ThinkingEnabled {
		t.Error("Default AI.Openrouter.ThinkingEnabled should be true")
	}
	if config.AI.Tools.Enabled {
		t.Error("Default AI.Tools.Enabled should be false")
	}
	if config.AI.Tools.MaxRounds != 3 {
		t.Errorf("Default AI.Tools.MaxRounds = %v, want 3", config.AI.Tools.MaxRounds)
	}
	if config.AI.Tools.Timeout != 10*time.Second {
		t.Errorf("Default AI.Tools.Timeout = %v, want 10s", config.AI.Tools.Timeout)
	}
	if !config.AI.Tools.Web.Enabled {
		t.Error("Default AI.Tools.Web.Enabled should be true")
	}
	if config.AI.Tools.Web.MaxCharacters != 12000 {
		t.Errorf("Default AI.Tools.Web.MaxCharacters = %v, want 12000", config.AI.Tools.Web.MaxCharacters)
	}
	if config.AI.Tools.Search.Enabled {
		t.Error("Default AI.Tools.Search.Enabled should be false")
	}
	if config.AI.Tools.Search.Provider != "searxng" {
		t.Errorf("Default AI.Tools.Search.Provider = %v, want searxng", config.AI.Tools.Search.Provider)
	}
	if config.AI.Tools.Search.MaxResults != 5 {
		t.Errorf("Default AI.Tools.Search.MaxResults = %v, want 5", config.AI.Tools.Search.MaxResults)
	}
	if config.AI.Retry.MaxRetries != 2 {
		t.Errorf("Default Retry.MaxRetries = %v, want 2", config.AI.Retry.MaxRetries)
	}
	if config.Memory.BufferThreshold != 10 {
		t.Errorf("Default Memory.BufferThreshold = %v, want 10", config.Memory.BufferThreshold)
	}
	if len(config.Bot.TriggerWords) != 2 || config.Bot.TriggerWords[0] != "feun" || config.Bot.TriggerWords[1] != "feunboy" {
		t.Errorf("Default Bot.TriggerWords = %v, want [feun feunboy]", config.Bot.TriggerWords)
	}
}

func TestVotesConfigDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Votes.Enabled || cfg.Votes.Probability != 0.25 || cfg.Votes.GoodEmoji != "😂" || cfg.Votes.BadEmoji != "🗑️" {
		t.Fatalf("unexpected vote defaults: %+v", cfg.Votes)
	}

	cfg.Votes.Probability = 1.1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "votes.probability") {
		t.Fatalf("Validate() error = %v, want votes.probability range error", err)
	}
}

func TestMediaLibraryConfigDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MediaLibrary.Enabled ||
		cfg.MediaLibrary.MinSimilarity != 0.80 ||
		cfg.MediaLibrary.MinTriggerSimilarity != 0.80 ||
		cfg.MediaLibrary.CrossGuild ||
		cfg.MediaLibrary.AllowDiscordCDN ||
		cfg.MediaLibrary.RepeatWindow != 20 ||
		cfg.MediaLibrary.LookupTimeoutMs != 3000 ||
		cfg.MediaLibrary.RandomGifFallback ||
		cfg.MediaLibrary.GifOnRequest ||
		cfg.MediaLibrary.ValidateLinks {
		t.Fatalf("unexpected media_library defaults: %+v", cfg.MediaLibrary)
	}

	// Valid edge values (0 and 1 for similarity, 0..200 for repeat_window, >=1 for lookup_timeout_ms).
	valid := DefaultConfig()
	valid.Discord.Token = "test-token"
	valid.Database.Local = true
	valid.AI.Groq.APIKey = "test-key"
	valid.MediaLibrary = MediaLibraryConfig{
		Enabled:              true,
		MinSimilarity:        0,
		MinTriggerSimilarity: 1,
		CrossGuild:           true,
		AllowDiscordCDN:      true,
		RepeatWindow:         200,
		LookupTimeoutMs:      1,
		RandomGifFallback:    true,
		GifOnRequest:         true,
		ValidateLinks:        true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error for valid boundary config: %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*MediaLibraryConfig)
		wantErr string
	}{
		{
			name:    "min_similarity below 0",
			mutate:  func(c *MediaLibraryConfig) { c.MinSimilarity = -0.01 },
			wantErr: "media_library.min_similarity",
		},
		{
			name:    "min_similarity above 1",
			mutate:  func(c *MediaLibraryConfig) { c.MinSimilarity = 1.01 },
			wantErr: "media_library.min_similarity",
		},
		{
			name:    "min_trigger_similarity below 0",
			mutate:  func(c *MediaLibraryConfig) { c.MinTriggerSimilarity = -0.1 },
			wantErr: "media_library.min_trigger_similarity",
		},
		{
			name:    "min_trigger_similarity above 1",
			mutate:  func(c *MediaLibraryConfig) { c.MinTriggerSimilarity = 1.5 },
			wantErr: "media_library.min_trigger_similarity",
		},
		{
			name:    "negative repeat_window",
			mutate:  func(c *MediaLibraryConfig) { c.RepeatWindow = -1 },
			wantErr: "media_library.repeat_window",
		},
		{
			name:    "repeat_window above 200",
			mutate:  func(c *MediaLibraryConfig) { c.RepeatWindow = 201 },
			wantErr: "media_library.repeat_window",
		},
		{
			name:    "zero lookup_timeout_ms",
			mutate:  func(c *MediaLibraryConfig) { c.LookupTimeoutMs = 0 },
			wantErr: "media_library.lookup_timeout_ms",
		},
		{
			name:    "negative lookup_timeout_ms",
			mutate:  func(c *MediaLibraryConfig) { c.LookupTimeoutMs = -100 },
			wantErr: "media_library.lookup_timeout_ms",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Discord.Token = "test-token"
			c.Database.Local = true
			c.AI.Groq.APIKey = "test-key"
			tc.mutate(&c.MediaLibrary)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}

	// Verify YAML loading preserves defaults for unspecified fields and overrides specified ones.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	t.Setenv("DISCORD_TOKEN", "test-token")
	t.Setenv("GROQ_API_KEY", "test-key")
	yamlContent := `
discord:
  token: "${DISCORD_TOKEN}"
database:
  local: true
ai:
  groq:
    api_key: "${GROQ_API_KEY}"
media_library:
  enabled: true
  min_similarity: 0.75
  cross_guild: true
  repeat_window: 10
  lookup_timeout_ms: 1500
  random_gif_fallback: true
  gif_on_request: true
  validate_links: true
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.MediaLibrary.Enabled ||
		loaded.MediaLibrary.MinSimilarity != 0.75 ||
		loaded.MediaLibrary.MinTriggerSimilarity != 0.80 ||
		!loaded.MediaLibrary.CrossGuild ||
		loaded.MediaLibrary.AllowDiscordCDN ||
		loaded.MediaLibrary.RepeatWindow != 10 ||
		loaded.MediaLibrary.LookupTimeoutMs != 1500 ||
		!loaded.MediaLibrary.RandomGifFallback ||
		!loaded.MediaLibrary.GifOnRequest ||
		!loaded.MediaLibrary.ValidateLinks {
		t.Fatalf("unexpected loaded media_library config: %+v", loaded.MediaLibrary)
	}

	// Verify omitting optional boolean keys keeps them false.
	yamlMinimal := `
discord:
  token: "${DISCORD_TOKEN}"
database:
  local: true
ai:
  groq:
    api_key: "${GROQ_API_KEY}"
media_library:
  enabled: true
`
	minimalPath := filepath.Join(tmpDir, "minimal.yaml")
	if err := os.WriteFile(minimalPath, []byte(yamlMinimal), 0644); err != nil {
		t.Fatal(err)
	}
	loadedMinimal, err := Load(minimalPath)
	if err != nil {
		t.Fatalf("Load() minimal error = %v", err)
	}
	if loadedMinimal.MediaLibrary.RandomGifFallback ||
		loadedMinimal.MediaLibrary.GifOnRequest ||
		loadedMinimal.MediaLibrary.ValidateLinks {
		t.Fatalf("expected omitted random_gif_fallback, gif_on_request, and validate_links to default false, got %+v", loadedMinimal.MediaLibrary)
	}
}

func TestLocalDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Setenv("DISCORD_TOKEN", "test-token")
	t.Setenv("GROQ_API_KEY", "test-key")

	// Config with local database - should not require URL or token
	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  local: true

ai:
  groq:
    api_key: "${GROQ_API_KEY}"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !config.Database.Local {
		t.Error("Database.Local should be true")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && containsString(s[1:], substr)) || s[:len(substr)] == substr)
}

func TestMemoryConfigNoMaxContextMessages(t *testing.T) {
	// max_context_messages was removed; loading a config that still contains
	// the key must not fail (unknown YAML keys are ignored), and the parsed
	// MemoryConfig must have no MaxContextMessages field to populate.
	cfg := &Config{}
	if err := cfg.Memory.validate(&cfg.AI); err != nil {
		t.Fatalf("disabled memory should pass validation: %v", err)
	}
	// Compile-time proof the field no longer exists:
	// cfg.Memory.MaxContextMessages would fail to compile.
}

func TestVisionProviderValidation(t *testing.T) {
	baseConfig := func() *Config {
		cfg := DefaultConfig()
		cfg.Discord.Token = "test-token"
		cfg.Database.Local = true
		cfg.Memory.Enabled = false
		return cfg
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
		errMsg  string
	}{
		{
			name: "empty vision_provider preserves old requirement for ollama vision_model on ollama primary",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.VisionProvider = ""
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
			},
			wantErr: true,
			errMsg:  "ai.ollama.vision_model cannot be empty when ollama is enabled",
		},
		{
			name: "empty vision_provider preserves old requirement for ollama vision_model on groq primary",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderGroq
				cfg.AI.VisionProvider = ""
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
			},
			wantErr: true,
			errMsg:  "ai.ollama.vision_model cannot be empty when ollama is enabled",
		},
		{
			name: "empty vision_provider with ollama primary and vision_model passes",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.VisionProvider = ""
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = "llava"
			},
			wantErr: false,
		},
		{
			name: "unknown vision_provider rejected",
			mutate: func(cfg *Config) {
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.VisionProvider = "anthropic"
			},
			wantErr: true,
			errMsg:  "ai.vision_provider must be one of",
		},
		{
			name: "disabled opencode vision_provider rejected",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = true
				cfg.AI.VisionProvider = AIProviderOpencode
				cfg.AI.Opencode.Enabled = false
			},
			wantErr: true,
			errMsg:  `ai.opencode.enabled must be true when ai.vision_provider is "opencode"`,
		},
		{
			name: "disabled ollama vision_provider rejected",
			mutate: func(cfg *Config) {
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.VisionProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = false
			},
			wantErr: true,
			errMsg:  `ai.ollama.enabled must be true when ai.vision_provider is "ollama"`,
		},
		{
			name: "disabled openrouter vision_provider rejected",
			mutate: func(cfg *Config) {
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.VisionProvider = AIProviderOpenrouter
				cfg.AI.Openrouter.Enabled = false
			},
			wantErr: true,
			errMsg:  `ai.openrouter.enabled must be true when ai.vision_provider is "openrouter"`,
		},
		{
			name: "groq vision_provider without api_key rejected",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = true
				cfg.AI.VisionProvider = AIProviderGroq
				cfg.AI.Groq.APIKey = ""
			},
			wantErr: true,
			errMsg:  `ai.groq.api_key is required when ai.vision_provider is "groq"`,
		},
		{
			name: "opencode vision_provider with empty vision_model rejected",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
				cfg.AI.VisionProvider = AIProviderOpencode
				cfg.AI.Opencode.Enabled = true
				cfg.AI.Opencode.APIKey = "opencode-key"
				cfg.AI.Opencode.VisionModel = ""
			},
			wantErr: true,
			errMsg:  "ai.opencode.vision_model cannot be empty when opencode is enabled",
		},
		{
			name: "openrouter vision_provider with empty vision_model rejected",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
				cfg.AI.VisionProvider = AIProviderOpenrouter
				cfg.AI.Openrouter.Enabled = true
				cfg.AI.Openrouter.APIKey = "openrouter-key"
				cfg.AI.Openrouter.VisionModel = ""
			},
			wantErr: true,
			errMsg:  "ai.openrouter.vision_model cannot be empty when openrouter is enabled",
		},
		{
			name: "groq vision_provider with empty vision_model rejected",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
				cfg.AI.VisionProvider = AIProviderGroq
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.Groq.VisionModel = ""
			},
			wantErr: true,
			errMsg:  "ai.groq.vision_model cannot be empty",
		},
		{
			name: "ollama vision_provider with empty vision_model rejected",
			mutate: func(cfg *Config) {
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.VisionProvider = AIProviderOllama
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
			},
			wantErr: true,
			errMsg:  "ai.ollama.vision_model cannot be empty when ollama is enabled",
		},
		{
			name: "ollama primary with opencode vision_provider allows empty ollama vision_model",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.VisionProvider = AIProviderOpencode
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
				cfg.AI.Opencode.Enabled = true
				cfg.AI.Opencode.APIKey = "opencode-key"
				cfg.AI.Opencode.VisionModel = "deepseek-v4.1-flash"
			},
			wantErr: false,
		},
		{
			name: "ollama primary with groq vision_provider allows empty ollama vision_model",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.VisionProvider = AIProviderGroq
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.Groq.VisionModel = "meta-llama/llama-4-scout-17b-16e-instruct"
			},
			wantErr: false,
		},
		{
			name: "ollama primary with openrouter vision_provider allows empty ollama vision_model",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderOllama
				cfg.AI.VisionProvider = AIProviderOpenrouter
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
				cfg.AI.Openrouter.Enabled = true
				cfg.AI.Openrouter.APIKey = "openrouter-key"
				cfg.AI.Openrouter.VisionModel = "google/gemini-2.5-flash"
			},
			wantErr: false,
		},
		{
			name: "groq primary and groq vision_provider allows enabled ollama with empty vision_model",
			mutate: func(cfg *Config) {
				cfg.AI.PrimaryProvider = AIProviderGroq
				cfg.AI.VisionProvider = AIProviderGroq
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.Ollama.Enabled = true
				cfg.AI.Ollama.VisionModel = ""
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestLoadVisionProviderFromYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	t.Setenv("DISCORD_TOKEN", "test-token")
	t.Setenv("OPENCODE_API_KEY", "test-opencode-key")

	configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  local: true

memory:
  enabled: false

ai:
  primary_provider: "ollama"
  vision_provider: "opencode"
  ollama:
    enabled: true
    url: "http://localhost:11434"
    model: "luciole"
    vision_model: ""
  opencode:
    enabled: true
    base_url: "https://opencode.ai/inference/go/v1"
    api_key: "${OPENCODE_API_KEY}"
    model: "deepseek-v4.1-flash"
    vision_model: "deepseek-v4.1-flash"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AI.PrimaryProvider != AIProviderOllama {
		t.Errorf("AI.PrimaryProvider = %q, want %q", cfg.AI.PrimaryProvider, AIProviderOllama)
	}
	if cfg.AI.VisionProvider != AIProviderOpencode {
		t.Errorf("AI.VisionProvider = %q, want %q", cfg.AI.VisionProvider, AIProviderOpencode)
	}
	if cfg.AI.Ollama.VisionModel != "" {
		t.Errorf("AI.Ollama.VisionModel = %q, want empty", cfg.AI.Ollama.VisionModel)
	}
}

func TestFallbackOrderValidation(t *testing.T) {
	baseConfig := func() *Config {
		cfg := DefaultConfig()
		cfg.Discord.Token = "test-token"
		cfg.Database.Local = true
		cfg.Memory.Enabled = false
		cfg.AI.PrimaryProvider = AIProviderOllama
		cfg.AI.Ollama.Enabled = true
		return cfg
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
		errMsg  string
	}{
		{
			name: "nil fallback_order is valid",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = nil
			},
			wantErr: false,
		},
		{
			name: "empty fallback_order is valid",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = []string{}
			},
			wantErr: false,
		},
		{
			name: "valid custom fallback_order",
			mutate: func(cfg *Config) {
				cfg.AI.Opencode.Enabled = true
				cfg.AI.Opencode.APIKey = "opencode-key"
				cfg.AI.Groq.APIKey = "groq-key"
				cfg.AI.FallbackOrder = []string{AIProviderOpencode, AIProviderGroq}
			},
			wantErr: false,
		},
		{
			name: "primary provider listed in fallback_order is allowed",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = []string{AIProviderOllama, AIProviderOpencode}
			},
			wantErr: false,
		},
		{
			name: "disabled provider listed in fallback_order is allowed without requiring its credentials",
			mutate: func(cfg *Config) {
				cfg.AI.Groq.APIKey = ""
				cfg.AI.Opencode.Enabled = false
				cfg.AI.Opencode.APIKey = ""
				cfg.AI.Openrouter.Enabled = false
				cfg.AI.Openrouter.APIKey = ""
				cfg.AI.FallbackOrder = []string{AIProviderOpencode, AIProviderOpenrouter, AIProviderGroq}
			},
			wantErr: false,
		},
		{
			name: "unknown provider name fails validation",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = []string{AIProviderOpencode, "anthropic"}
			},
			wantErr: true,
			errMsg:  `ai.fallback_order contains unknown provider "anthropic"`,
		},
		{
			name: "empty provider name fails validation",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = []string{""}
			},
			wantErr: true,
			errMsg:  `ai.fallback_order contains unknown provider ""`,
		},
		{
			name: "duplicate provider name fails validation",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = []string{AIProviderOpencode, AIProviderGroq, AIProviderOpencode}
			},
			wantErr: true,
			errMsg:  `ai.fallback_order contains duplicate provider "opencode"`,
		},
		{
			name: "duplicate primary provider fails validation",
			mutate: func(cfg *Config) {
				cfg.AI.FallbackOrder = []string{AIProviderOllama, AIProviderOllama}
			},
			wantErr: true,
			errMsg:  `ai.fallback_order contains duplicate provider "ollama"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestLoadFallbackOrderFromYAML(t *testing.T) {
	t.Run("loads configured fallback_order list", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		t.Setenv("DISCORD_TOKEN", "test-token")
		t.Setenv("OPENCODE_API_KEY", "test-opencode-key")
		t.Setenv("GROQ_API_KEY", "test-groq-key")

		configContent := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  local: true

memory:
  enabled: false

ai:
  primary_provider: "ollama"
  fallback_enabled: true
  fallback_order: ["opencode", "groq"]
  ollama:
    enabled: true
    url: "http://localhost:11434"
    model: "luciole"
    vision_model: "llava"
  opencode:
    enabled: true
    base_url: "https://opencode.ai/inference/go/v1"
    api_key: "${OPENCODE_API_KEY}"
    model: "deepseek-v4.1-flash"
    vision_model: "deepseek-v4.1-flash"
  groq:
    api_key: "${GROQ_API_KEY}"
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("Failed to write config file: %v", err)
		}

		cfg, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(cfg.AI.FallbackOrder) != 2 || cfg.AI.FallbackOrder[0] != AIProviderOpencode || cfg.AI.FallbackOrder[1] != AIProviderGroq {
			t.Errorf("AI.FallbackOrder = %v, want [opencode groq]", cfg.AI.FallbackOrder)
		}
	})

	t.Run("rejects unknown or duplicate fallback_order entries from YAML", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("DISCORD_TOKEN", "test-token")
		t.Setenv("GROQ_API_KEY", "test-groq-key")

		for _, tc := range []struct {
			name    string
			order   string
			wantErr string
		}{
			{name: "unknown", order: `["bogus"]`, wantErr: `ai.fallback_order contains unknown provider "bogus"`},
			{name: "duplicate", order: `["opencode", "opencode"]`, wantErr: `ai.fallback_order contains duplicate provider "opencode"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				configPath := filepath.Join(tmpDir, tc.name+".yaml")
				content := `
discord:
  token: "${DISCORD_TOKEN}"

database:
  local: true

memory:
  enabled: false

ai:
  fallback_order: ` + tc.order + `
  groq:
    api_key: "${GROQ_API_KEY}"
`
				if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
					t.Fatalf("Failed to write config file: %v", err)
				}
				_, err := Load(configPath)
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load() error = %v, want substring %q", err, tc.wantErr)
				}
			})
		}
	})
}

func TestWebConfigDefaultsAndValidation(t *testing.T) {
	defaults := DefaultConfig()
	if defaults.Web.Enabled {
		t.Errorf("DefaultConfig().Web.Enabled = true, want false")
	}
	if defaults.Web.Listen != "127.0.0.1:8090" {
		t.Errorf("DefaultConfig().Web.Listen = %q, want %q", defaults.Web.Listen, "127.0.0.1:8090")
	}
	if len(defaults.Web.AllowedHosts) != 0 {
		t.Errorf("DefaultConfig().Web.AllowedHosts = %v, want empty", defaults.Web.AllowedHosts)
	}

	makeBase := func() *Config {
		cfg := DefaultConfig()
		cfg.Discord.Token = "test-token"
		cfg.Database.Local = true
		cfg.AI.Groq.APIKey = "test-key"
		cfg.Memory.Enabled = false
		return cfg
	}

	t.Run("disabled ignores invalid listen", func(t *testing.T) {
		cfg := makeBase()
		cfg.Web.Enabled = false
		cfg.Web.Listen = "not-a-host-port"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() unexpected error when web disabled: %v", err)
		}
	})

	t.Run("enabled accepts valid listen and allowed_hosts", func(t *testing.T) {
		cfg := makeBase()
		cfg.Web.Enabled = true
		cfg.Web.Listen = "127.0.0.1:8090"
		cfg.Web.AllowedHosts = []string{"admin.local", "10.0.0.5"}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("enabled accepts ipv6 listen", func(t *testing.T) {
		cfg := makeBase()
		cfg.Web.Enabled = true
		cfg.Web.Listen = "[::1]:8090"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("enabled rejects invalid listen", func(t *testing.T) {
		for _, listen := range []string{"", "127.0.0.1", "127.0.0.1:", "127.0.0.1:abc", "127.0.0.1:70000"} {
			cfg := makeBase()
			cfg.Web.Enabled = true
			cfg.Web.Listen = listen
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate() accepted invalid web.listen %q", listen)
			}
		}
	})

	t.Run("enabled rejects empty allowed_hosts entry", func(t *testing.T) {
		cfg := makeBase()
		cfg.Web.Enabled = true
		cfg.Web.Listen = "127.0.0.1:8090"
		cfg.Web.AllowedHosts = []string{"   "}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "web.allowed_hosts") {
			t.Fatalf("Validate() error = %v, want web.allowed_hosts error", err)
		}
	})

	t.Run("enabled rejects allowed_hosts with scheme or path", func(t *testing.T) {
		for _, host := range []string{"https://admin.example", "admin.example/path", "user@admin.example"} {
			cfg := makeBase()
			cfg.Web.Enabled = true
			cfg.Web.AllowedHosts = []string{host}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "web.allowed_hosts") {
				t.Errorf("Validate() accepted allowed_hosts entry %q (err=%v)", host, err)
			}
		}
	})

	t.Run("loads web section from YAML", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		t.Setenv("DISCORD_TOKEN", "test-token")
		t.Setenv("GROQ_API_KEY", "test-groq-key")

		content := `
discord:
  token: "${DISCORD_TOKEN}"
database:
  local: true
memory:
  enabled: false
ai:
  groq:
    api_key: "${GROQ_API_KEY}"
web:
  enabled: true
  listen: "127.0.0.1:9090"
  allowed_hosts:
    - "bot.tailnet.ts.net"
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !cfg.Web.Enabled || cfg.Web.Listen != "127.0.0.1:9090" || len(cfg.Web.AllowedHosts) != 1 || cfg.Web.AllowedHosts[0] != "bot.tailnet.ts.net" {
			t.Fatalf("unexpected web config: %+v", cfg.Web)
		}
	})
}
