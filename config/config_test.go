package config

import (
	"os"
	"path/filepath"
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
				Discord: DiscordConfig{Token: "test-token"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test-token", Local: false},
				AI: AIConfig{
					Groq: GroqConfig{APIKey: "test-key", Model: "model", VisionModel: "vision"},
					Ollama: OllamaConfig{Enabled: false},
					Retry: RetryConfig{
						MaxRetries:      2,
						InitialDelay:    500 * time.Millisecond,
						MaxDelay:        5 * time.Second,
						BackoffFactor:   2.0,
						InitialDelayMs:  500,
						MaxDelayMs:      5000,
					},
					MinResponseLength: 1,
				},
				Logging: LoggingConfig{Level: "info", Encoding: "json", RetentionDays: 7, DBLogLevel: "info"},
				Memory: MemoryConfig{
					OllamaURL:              "http://localhost",
					EmbeddingModel:         "model",
					SummaryModel:           "model",
					BufferThreshold:        10,
					SummaryInterval:        1 * time.Hour,
					SummaryIntervalSeconds: 3600,
					MaxContextMessages:     5,
					MaxSummaryContext:       3,
				},
				Emoji:     EmojiConfig{CacheTTLMinutes: 60},
				Horoscope: HoroscopeConfig{IncludeEmojis: true},
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
				Discord: DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "", Token: "", Local: false},
			},
			wantErr: true,
			errMsg:  "database.url is required",
		},
		{
			name: "missing groq api key",
			config: &Config{
				Discord: DiscordConfig{Token: "test"},
				Database: DatabaseConfig{URL: "http://localhost", Token: "test", Local: false},
				AI: AIConfig{
					Groq: GroqConfig{APIKey: ""},
				},
			},
			wantErr: true,
			errMsg:  "ai.groq.api_key is required",
		},
		{
			name: "invalid log level",
			config: &Config{
				Discord: DiscordConfig{Token: "test"},
				Database: DatabaseConfig{Local: true},
				AI: AIConfig{
					Groq: GroqConfig{APIKey: "test", Model: "model", VisionModel: "vision"},
					Retry: RetryConfig{InitialDelay: 1 * time.Millisecond, MaxDelay: 2 * time.Millisecond, BackoffFactor: 2.0},
				},
				Logging: LoggingConfig{Level: "invalid", Encoding: "json", DBLogLevel: "info"},
			},
			wantErr: true,
			errMsg:  "logging.level must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
	if config.AI.Retry.MaxRetries != 2 {
		t.Errorf("Default Retry.MaxRetries = %v, want 2", config.AI.Retry.MaxRetries)
	}
	if config.Memory.BufferThreshold != 10 {
		t.Errorf("Default Memory.BufferThreshold = %v, want 10", config.Memory.BufferThreshold)
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