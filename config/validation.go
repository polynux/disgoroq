package config

import (
	"fmt"
	"strings"
	"time"

	"polynux/disgoroq/triggerwords"
)

// Validate checks if the configuration is valid and returns an error describing any issues.
func (c *Config) Validate() error {
	var errors []string

	if err := c.Discord.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Database.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.AI.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Logging.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Memory.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Emoji.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Horoscope.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Reengage.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Bot.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if err := c.Voice.validate(); err != nil {
		errors = append(errors, err.Error())
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration errors:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

func (c *DiscordConfig) validate() error {
	if c.Token == "" {
		return fmt.Errorf("discord.token is required")
	}
	for _, guildID := range c.DevGuildIDs {
		if strings.TrimSpace(guildID) == "" {
			return fmt.Errorf("discord.dev_guild_ids cannot contain empty values")
		}
	}
	return nil
}

func (c *DatabaseConfig) validate() error {
	// Local database doesn't require URL or token
	if c.Local {
		return nil
	}

	if c.URL == "" {
		return fmt.Errorf("database.url is required when not using local database")
	}
	if c.Token == "" {
		return fmt.Errorf("database.token is required when not using local database")
	}
	return nil
}

func (c *AIConfig) validate() error {
	switch c.PrimaryProvider {
	case AIProviderGroq, AIProviderOllama, AIProviderOpencode, AIProviderOpenrouter:
	default:
		return fmt.Errorf("ai.primary_provider must be one of %q, %q, %q, or %q", AIProviderGroq, AIProviderOllama, AIProviderOpencode, AIProviderOpenrouter)
	}

	if c.PrimaryProvider == AIProviderGroq {
		if c.Groq.APIKey == "" {
			return fmt.Errorf("ai.groq.api_key is required when ai.primary_provider is %q", AIProviderGroq)
		}
	}

	if c.shouldValidateGroq() {
		if err := c.Groq.validate(); err != nil {
			return err
		}
	}

	if c.PrimaryProvider == AIProviderOllama && !c.Ollama.Enabled {
		return fmt.Errorf("ai.ollama.enabled must be true when ai.primary_provider is %q", AIProviderOllama)
	}

	if c.shouldValidateOllama() {
		if err := c.Ollama.validate(); err != nil {
			return err
		}
	}

	if c.PrimaryProvider == AIProviderOpencode && !c.Opencode.Enabled {
		return fmt.Errorf("ai.opencode.enabled must be true when ai.primary_provider is %q", AIProviderOpencode)
	}

	if c.shouldValidateOpencode() {
		if err := c.Opencode.validate(); err != nil {
			return err
		}
	}

	if c.PrimaryProvider == AIProviderOpenrouter && !c.Openrouter.Enabled {
		return fmt.Errorf("ai.openrouter.enabled must be true when ai.primary_provider is %q", AIProviderOpenrouter)
	}

	if c.shouldValidateOpenrouter() {
		if err := c.Openrouter.validate(); err != nil {
			return err
		}
	}

	if err := c.Retry.validate(); err != nil {
		return err
	}

	if c.MinResponseLength < 1 {
		return fmt.Errorf("ai.min_response_length must be at least 1, got %d", c.MinResponseLength)
	}

	return nil
}

func (c *AIConfig) shouldValidateGroq() bool {
	return c.PrimaryProvider == AIProviderGroq || c.Groq.APIKey != ""
}

func (c *AIConfig) shouldValidateOllama() bool {
	return c.PrimaryProvider == AIProviderOllama || c.Ollama.Enabled
}

func (c *AIConfig) shouldValidateOpencode() bool {
	return c.PrimaryProvider == AIProviderOpencode || c.Opencode.Enabled
}

func (c *AIConfig) shouldValidateOpenrouter() bool {
	return c.PrimaryProvider == AIProviderOpenrouter || c.Openrouter.Enabled
}

func (c *GroqConfig) validate() error {
	if c.Model == "" {
		return fmt.Errorf("ai.groq.model cannot be empty")
	}
	if c.VisionModel == "" {
		return fmt.Errorf("ai.groq.vision_model cannot be empty")
	}
	return nil
}

func (c *OllamaConfig) validate() error {
	// Ollama is optional, only validate if enabled
	if !c.Enabled {
		return nil
	}

	if c.URL == "" {
		return fmt.Errorf("ai.ollama.url is required when ollama is enabled")
	}
	if c.Model == "" {
		return fmt.Errorf("ai.ollama.model cannot be empty when ollama is enabled")
	}
	if c.VisionModel == "" {
		return fmt.Errorf("ai.ollama.vision_model cannot be empty when ollama is enabled")
	}
	return nil
}

func (c *OpencodeConfig) validate() error {
	if !c.Enabled {
		return nil
	}

	if c.BaseURL == "" {
		return fmt.Errorf("ai.opencode.base_url is required when opencode is enabled")
	}
	if c.APIKey == "" {
		return fmt.Errorf("ai.opencode.api_key is required when opencode is enabled")
	}
	if c.Model == "" {
		return fmt.Errorf("ai.opencode.model cannot be empty when opencode is enabled")
	}
	if c.VisionModel == "" {
		return fmt.Errorf("ai.opencode.vision_model cannot be empty when opencode is enabled")
	}
	return nil
}

func (c *OpenrouterConfig) validate() error {
	if !c.Enabled {
		return nil
	}

	if c.BaseURL == "" {
		return fmt.Errorf("ai.openrouter.base_url is required when openrouter is enabled")
	}
	if c.APIKey == "" {
		return fmt.Errorf("ai.openrouter.api_key is required when openrouter is enabled")
	}
	if c.Model == "" {
		return fmt.Errorf("ai.openrouter.model cannot be empty when openrouter is enabled")
	}
	if c.VisionModel == "" {
		return fmt.Errorf("ai.openrouter.vision_model cannot be empty when openrouter is enabled")
	}
	return nil
}

func (c *RetryConfig) validate() error {
	if c.MaxRetries < 0 {
		return fmt.Errorf("ai.retry.max_retries cannot be negative: %d", c.MaxRetries)
	}
	if c.InitialDelay <= 0 {
		return fmt.Errorf("ai.retry.initial_delay_ms must be positive: %v", c.InitialDelay)
	}
	if c.MaxDelay <= 0 {
		return fmt.Errorf("ai.retry.max_delay_ms must be positive: %v", c.MaxDelay)
	}
	if c.BackoffFactor < 1.0 {
		return fmt.Errorf("ai.retry.backoff_factor must be >= 1.0: %f", c.BackoffFactor)
	}
	if c.InitialDelay > c.MaxDelay {
		return fmt.Errorf("ai.retry.initial_delay_ms (%v) cannot be greater than max_delay_ms (%v)", c.InitialDelay, c.MaxDelay)
	}
	return nil
}

func (c *LoggingConfig) validate() error {
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	if !validLevels[strings.ToLower(c.Level)] {
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error; got: %s", c.Level)
	}

	validEncodings := map[string]bool{
		"json":    true,
		"console": true,
	}

	if !validEncodings[strings.ToLower(c.Encoding)] {
		return fmt.Errorf("logging.encoding must be one of: json, console; got: %s", c.Encoding)
	}

	if c.RetentionDays < 1 {
		return fmt.Errorf("logging.retention_days must be at least 1, got: %d", c.RetentionDays)
	}

	validDBLogLevels := map[string]bool{
		"none":  true,
		"error": true,
		"warn":  true,
		"info":  true,
		"debug": true,
		"all":   true,
	}

	if !validDBLogLevels[strings.ToLower(c.DBLogLevel)] {
		return fmt.Errorf("logging.db_log_level must be one of: none, error, warn, info, debug, all; got: %s", c.DBLogLevel)
	}

	return nil
}

func (c *MemoryConfig) validate() error {
	// Memory is optional, only validate if enabled
	if !c.Enabled {
		return nil
	}

	if c.OllamaURL == "" {
		return fmt.Errorf("memory.ollama_url is required when memory is enabled")
	}
	if c.EmbeddingModel == "" {
		return fmt.Errorf("memory.embedding_model cannot be empty when memory is enabled")
	}
	if c.SummaryModel == "" {
		return fmt.Errorf("memory.summary_model cannot be empty when memory is enabled")
	}
	if c.BufferThreshold < 1 {
		return fmt.Errorf("memory.buffer_threshold must be at least 1, got: %d", c.BufferThreshold)
	}
	if c.SummaryInterval < time.Minute {
		return fmt.Errorf("memory.summary_interval_seconds must be at least 60 seconds (1 minute)")
	}
	if c.MaxContextMessages < 1 {
		return fmt.Errorf("memory.max_context_messages must be at least 1, got: %d", c.MaxContextMessages)
	}
	if c.MaxSummaryContext < 1 {
		return fmt.Errorf("memory.max_summary_context must be at least 1, got: %d", c.MaxSummaryContext)
	}

	return nil
}

func (c *EmojiConfig) validate() error {
	if c.CacheTTLMinutes < 1 {
		return fmt.Errorf("emoji.cache_ttl_minutes must be at least 1, got: %d", c.CacheTTLMinutes)
	}
	return nil
}

func (c *HoroscopeConfig) validate() error {
	// No validation needed for boolean
	return nil
}

func (c *ReengageConfig) validate() error {
	if c.CheckIntervalSeconds < 0 {
		return fmt.Errorf("reengage.check_interval_seconds cannot be negative")
	}
	if c.DefaultInactivityMinutes < 1 {
		return fmt.Errorf("reengage.default_inactivity_minutes must be at least 1")
	}
	if c.DefaultChance < 0 || c.DefaultChance > 1 {
		return fmt.Errorf("reengage.default_chance must be between 0 and 1")
	}
	return nil
}

func (c *BotConfig) validate() error {
	if strings.TrimSpace(c.DefaultPrompt) == "" {
		return fmt.Errorf("bot.default_prompt is required")
	}
	if err := triggerwords.Validate(c.TriggerWords); err != nil {
		return fmt.Errorf("bot.trigger_words is invalid: %w", err)
	}
	return nil
}

func (c *VoiceConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.TTS.Endpoint) == "" {
		return fmt.Errorf("voice.tts.endpoint is required when voice is enabled")
	}
	if c.TTS.TimeoutMs <= 0 {
		return fmt.Errorf("voice.tts.timeout_ms must be positive")
	}
	if c.Audio.SampleRate <= 0 {
		return fmt.Errorf("voice.audio.sample_rate must be positive")
	}
	if c.Audio.Channels <= 0 {
		return fmt.Errorf("voice.audio.channels must be positive")
	}
	if c.Audio.VADSilenceMs < 0 || c.Audio.VADSpeechMinMs < 0 || c.Audio.VADMaxDurationMs < 0 {
		return fmt.Errorf("voice audio VAD timings cannot be negative")
	}
	if c.Audio.VADAmplitudeThreshold < 0 || c.Audio.VADAmplitudeThreshold > 1 {
		return fmt.Errorf("voice.audio.vad_amplitude_threshold must be between 0 and 1")
	}
	if strings.TrimSpace(c.STT.SocketPath) == "" {
		return fmt.Errorf("voice.stt.socket_path is required when voice is enabled")
	}
	return nil
}

// GetDelayForAttempt calculates the delay for a specific retry attempt using exponential backoff.
func (c *RetryConfig) GetDelayForAttempt(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// Calculate exponential backoff delay
	delay := float64(c.InitialDelay) * pow(c.BackoffFactor, float64(attempt-1))

	// Cap at maximum delay
	if time.Duration(delay) > c.MaxDelay {
		return c.MaxDelay
	}

	return time.Duration(delay)
}

// pow calculates base^exp for float64
func pow(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}

// String returns a string representation of the retry configuration.
func (c RetryConfig) String() string {
	return fmt.Sprintf("RetryConfig{MaxRetries:%d, InitialDelay:%v, MaxDelay:%v, BackoffFactor:%.1f, RetryOnEmpty:%t, RetryOnError:%t}",
		c.MaxRetries, c.InitialDelay, c.MaxDelay, c.BackoffFactor, c.RetryOnEmpty, c.RetryOnError)
}
