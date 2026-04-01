package voice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
)

// Orchestrator manages the voice conversation flow.
// It coordinates between STT (speech-to-text), AI service, and TTS (text-to-speech).
type Orchestrator struct {
	manager           *Manager
	sttClient         STTClient
	ttsClient         TTSClient
	aiService         *ai.Service
	memoryService     memory.Service
	contextBuilder    *ai.ContextBuilder
	client            *bot.Client
	repo              Repository
	defaultPrompt     string
	voiceSystemPrompt string // Separate prompt for voice mode
	config            config.VoiceConfig

	// State management
	sessions   map[string]*VoiceConversation // guildID -> conversation
	sessionsMu sync.RWMutex

	// VRAM management
	lastTTSAudio time.Time
	vramCheckMu  sync.Mutex

	// Callbacks
	onStateChange func(guildID string, oldState, newState AgentState)
}

// Repository defines the interface for voice-related database operations.
type Repository interface {
	GetVoiceEnabled(ctx context.Context, guildID string) bool
	GetTemperature(ctx context.Context, guildID string) float32
	GetPrompt(ctx context.Context, guildID string) (string, bool)
	GetVoicePrompt(ctx context.Context, guildID string) (string, bool)
}

// VoiceConversation represents an active voice conversation in a guild.
type VoiceConversation struct {
	GuildID       string
	TextChannelID string
	State         AgentState
	AudioBuffer   *AudioBufferManager
	LastActivity  time.Time
	CancelFunc    context.CancelFunc

	// History tracks voice conversation messages for context
	History *VoiceHistoryManager

	// StateManager handles idle timeout and audio buffering during pauses
	StateManager *VoiceStateManager

	// LastSpeakerID tracks the most recent speaker
	LastSpeakerID string

	// SilenceCheckCancel cancels the silence checking goroutine
	SilenceCheckCancel context.CancelFunc

	mu         sync.Mutex
	processing bool
}

// OrchestratorConfig contains configuration for the orchestrator.
type OrchestratorConfig struct {
	STTClient         STTClient
	TTSClient         TTSClient
	AIService         *ai.Service
	MemoryService     memory.Service
	Client            *bot.Client
	Repository        Repository
	DefaultPrompt     string
	VoiceConfig       config.VoiceConfig
	VoiceSystemPrompt string // Separate system prompt for voice mode
}

// NewOrchestrator creates a new voice orchestrator.
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	// Use VoiceSystemPrompt from config if set, otherwise use DefaultPrompt
	voicePrompt := cfg.VoiceSystemPrompt
	if voicePrompt == "" {
		voicePrompt = cfg.DefaultPrompt
	}

	o := &Orchestrator{
		manager:           NewManager(cfg.Client, cfg.VoiceConfig),
		sttClient:         cfg.STTClient,
		ttsClient:         cfg.TTSClient,
		aiService:         cfg.AIService,
		memoryService:     cfg.MemoryService,
		contextBuilder:    ai.NewContextBuilder(cfg.Client, cfg.AIService),
		client:            cfg.Client,
		repo:              cfg.Repository,
		defaultPrompt:     cfg.DefaultPrompt,
		voiceSystemPrompt: voicePrompt,
		config:            cfg.VoiceConfig,
		sessions:          make(map[string]*VoiceConversation),
	}

	// Set up audio callback
	o.manager.OnSpeak(o.handleAudio)
	o.manager.onJoin = o.handleJoin
	o.manager.onLeave = o.handleLeave

	return o
}

// JoinVoice connects to a voice channel and starts listening.
func (o *Orchestrator) JoinVoice(ctx context.Context, guildID, channelID, textChannelID string) error {
	if o.repo != nil && !o.repo.GetVoiceEnabled(ctx, guildID) {
		return ErrVoiceDisabled
	}

	// Check if already connected
	if o.manager.IsConnected(guildID) {
		return ErrAlreadyConnected
	}

	// Join the voice channel
	if err := o.manager.JoinVoice(ctx, guildID, channelID, textChannelID); err != nil {
		return err
	}

	// Create conversation session
	// Calculate idle timeout from config (default to 3000ms if not set)
	idleTimeoutMs := o.config.IdleTimeoutMs
	if idleTimeoutMs <= 0 {
		idleTimeoutMs = 3000 // Default: 3 seconds
	}

	// Get max history length from config (default to 10 if not set)
	maxHistoryLength := o.config.VoiceContextMaxLength
	if maxHistoryLength <= 0 {
		maxHistoryLength = 10 // Default: 10 messages
	}

	conv := &VoiceConversation{
		GuildID:       guildID,
		TextChannelID: textChannelID,
		State:         StateListening,
		AudioBuffer: NewAudioBufferManager(
			o.config.Audio.SampleRate,
			o.config.Audio.Channels,
			max(1, int(o.config.Audio.FrameDuration()/time.Millisecond)),
		),
		LastActivity: time.Now(),
		History:      NewVoiceHistoryManager(maxHistoryLength),
		StateManager: NewVoiceStateManager(time.Duration(idleTimeoutMs) * time.Millisecond),
	}

	// Configure VAD threshold from config
	vadThreshold := o.config.Audio.VADAmplitudeThreshold
	if vadThreshold <= 0 {
		vadThreshold = 0.02 // Default 2% of max amplitude
	}
	conv.AudioBuffer.SetSilenceThresholdFromNormalized(vadThreshold)

	logger.Info("Voice session created with VAD settings",
		zap.String("guild_id", guildID),
		zap.Float64("vad_threshold", vadThreshold),
		zap.Int16("silence_level", conv.AudioBuffer.GetSilenceLevel()),
		zap.Int("silence_threshold_ms", o.config.Audio.VADSilenceMs),
		zap.Int("speech_min_ms", o.config.Audio.VADSpeechMinMs))

	// Start silence checker goroutine
	// Discord doesn't send packets during silence, so we need to detect it via timeout
	silenceCheckCtx, silenceCheckCancel := context.WithCancel(context.Background())
	conv.SilenceCheckCancel = silenceCheckCancel
	go o.silenceChecker(silenceCheckCtx, guildID, conv)

	o.sessionsMu.Lock()
	o.sessions[guildID] = conv
	o.sessionsMu.Unlock()

	// Preload TTS model in background
	go func() {
		bgCtx := context.Background()
		if err := o.ensureTTSLoaded(bgCtx); err != nil {
			logger.Warn("Failed to preload TTS model",
				zap.String("guild_id", guildID),
				zap.Error(err))
		}
	}()

	logger.Info("Voice orchestrator joined channel",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID))

	return nil
}

// LeaveVoice disconnects from a voice channel.
func (o *Orchestrator) LeaveVoice(guildID string) error {
	o.sessionsMu.Lock()
	if conv, exists := o.sessions[guildID]; exists {
		if conv.CancelFunc != nil {
			conv.CancelFunc()
		}
		if conv.SilenceCheckCancel != nil {
			conv.SilenceCheckCancel()
		}
		delete(o.sessions, guildID)
	}
	o.sessionsMu.Unlock()

	// Unload TTS model if auto-unload is enabled
	if o.config.VRAM.AutoUnload {
		go func() {
			time.Sleep(time.Duration(o.config.VRAM.UnloadTimeoutSeconds) * time.Second)
			o.maybeUnloadTTS()
		}()
	}

	return o.manager.LeaveVoice(context.Background(), guildID)
}

// ProcessVoiceInput processes transcribed voice input through the AI and responds.
func (o *Orchestrator) ProcessVoiceInput(ctx context.Context, userID, guildID, text string) error {
	o.sessionsMu.RLock()
	conv, exists := o.sessions[guildID]
	o.sessionsMu.RUnlock()

	if !exists {
		return ErrNotConnected
	}

	// Update state to thinking
	o.transitionState(guildID, StateThinking)

	// Get bot's nickname for prompt template
	guildSnowflake, err := snowflake.Parse(guildID)
	if err != nil {
		logger.Warn("Failed to parse guild ID", zap.Error(err))
	}

	var botNick string
	if err == nil {
		botMember, err := o.client.Rest.GetMember(guildSnowflake, o.client.ID())
		if err != nil {
			logger.Warn("Failed to get bot member", zap.Error(err))
		} else if botMember != nil && botMember.Nick != nil {
			botNick = *botMember.Nick
		}
	}

	// Build the system prompt - use voice-specific prompt
	var instructions string

	// Try to get custom voice prompt first (completely replaces the default)
	if prompt, ok := o.repo.GetVoicePrompt(ctx, guildID); ok {
		instructions = prompt
	} else {
		// Use the voice system prompt (already includes voice context instructions)
		instructions = o.voiceSystemPrompt

		// Apply bot nickname template if needed
		if strings.Contains(instructions, "{{.BotNick}}") {
			tmpl, err := template.New("prompt").Parse(instructions)
			if err == nil {
				var result strings.Builder
				data := map[string]string{"BotNick": botNick}
				if err := tmpl.Execute(&result, data); err == nil {
					instructions = result.String()
				}
			}
		}
	}

	// Get username for history
	username := userID // Default to user ID
	if guildSnowflake, err := snowflake.Parse(guildID); err == nil {
		userSnowflake, err := snowflake.Parse(userID)
		if err == nil {
			if member, err := o.client.Rest.GetMember(guildSnowflake, userSnowflake); err == nil {
				if member.Nick != nil && *member.Nick != "" {
					username = *member.Nick
				} else {
					username = member.User.Username
				}
			}
		}
	}

	// Add user message to voice history
	botID := o.client.ID().String()
	conv.History.AddMessage(userID, username, text, false)

	// Build voice context from history
	voiceContext := conv.History.FormatForContext()

	// Build memory context if available
	var memoryContext string
	if o.memoryService != nil {
		memoryCtx, err := o.memoryService.GetMemoryContext(ctx, userID, guildID, text)
		if err == nil && memoryCtx != nil && len(memoryCtx.Summaries) > 0 {
			memoryContext = "\n\n**Contexte de conversation:**\n"
			for i, summary := range memoryCtx.Summaries {
				if i < 2 {
					memoryContext += fmt.Sprintf("- %s\n", summary.Content)
				}
			}
		}
	}

	// Build messages including voice history for context
	var messages []ai.Message

	// Add voice history messages for context
	if voiceContext != "" {
		// Parse history and add to messages
		// History format: "[Username]: message\n"
		historyMsgs := conv.History.GetHistory()
		for _, msg := range historyMsgs {
			role := "user"
			if msg.IsBot {
				role = "assistant"
			}
			messages = append(messages, ai.Message{
				Role:     role,
				Content:  msg.Content,
				AuthorID: msg.UserID,
			})
		}
	}

	// Add current message
	messages = append(messages, ai.Message{
		Role:     "user",
		Content:  text,
		AuthorID: userID,
	})

	// Add memory context to system prompt
	systemPrompt := instructions
	if memoryContext != "" {
		systemPrompt += memoryContext
	}

	// Buffer message for memory
	if o.memoryService != nil {
		go func() {
			if err := o.memoryService.BufferMessage(context.Background(), userID, guildID, text); err != nil {
				logger.Warn("Failed to buffer voice message", zap.Error(err))
			}
		}()
	}

	// Get temperature
	temperature := o.repo.GetTemperature(ctx, guildID)

	// Call AI service
	response, err := o.aiService.Chat(ctx, &ai.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Temperature:  temperature,
		MaxTokens:    150, // Shorter responses for voice
	})

	if err != nil {
		logger.Error("AI chat failed in voice mode",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text
		fallbackMsg := "Désolé, j'ai eu un problème technique..."
		return o.manager.SendTextFallback(context.Background(), guildID, fallbackMsg)
	}

	if response.Content == "" {
		return o.manager.SendTextFallback(context.Background(), guildID, "Euh... je n'ai rien à dire...")
	}

	// Add bot response to voice history
	conv.History.AddMessage(botID, "Bot", response.Content, true)

	// Buffer response for memory
	if o.memoryService != nil {
		go func() {
			if err := o.memoryService.BufferMessage(context.Background(), botID, guildID, response.Content); err != nil {
				logger.Warn("Failed to buffer voice response", zap.Error(err))
			}
		}()
	}

	// Update last activity
	conv.LastActivity = time.Now()

	// Speak the response
	logger.Info("Starting TTS for voice response",
		zap.String("guild_id", guildID),
		zap.String("text", truncateText(response.Content, 100)))

	return o.Speak(ctx, guildID, response.Content)
}

// Speak generates TTS audio and plays it in the voice channel.
// Uses streaming generation when StaticMode is false (recommended for lower latency),
// or static generation when StaticMode is true.
func (o *Orchestrator) Speak(ctx context.Context, guildID, text string) error {
	logger.Info("Speak() called",
		zap.String("guild_id", guildID),
		zap.Int("text_len", len(text)),
		zap.Bool("static_mode", o.config.TTS.StaticMode))

	ttsText := o.limitTTSText(text)
	if ttsText == "" {
		return nil
	}

	// Update state to speaking
	o.transitionState(guildID, StateSpeaking)
	defer func() {
		o.transitionState(guildID, StateListening)

		// Mark speech end and start idle timeout
		o.sessionsMu.RLock()
		if conv, exists := o.sessions[guildID]; exists {
			conv.StateManager.MarkSpeechEnd()
		}
		o.sessionsMu.RUnlock()
	}()

	// Send text to chat first if AlwaysSendText is enabled
	if o.config.TTS.AlwaysSendText {
		if err := o.manager.SendTextFallback(context.Background(), guildID, text); err != nil {
			logger.Warn("Failed to send text fallback",
				zap.String("guild_id", guildID),
				zap.Error(err))
		}
	}

	// Ensure TTS model is loaded
	logger.Info("Checking TTS model availability", zap.String("guild_id", guildID))
	if err := o.ensureTTSLoaded(ctx); err != nil {
		logger.Error("Failed to load TTS model",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text (already sent if AlwaysSendText, but send anyway if not)
		if !o.config.TTS.AlwaysSendText {
			_ = o.manager.SendTextFallback(context.Background(), guildID, text)
		}
		return fmt.Errorf("failed to load TTS model: %w", err)
	}
	logger.Info("TTS model ready", zap.String("guild_id", guildID))

	// Check for context cancellation
	select {
	case <-ctx.Done():
		logger.Warn("Context cancelled before TTS", zap.String("guild_id", guildID))
		return ctx.Err()
	default:
	}

	// Use streaming mode for lower latency, or fallback to static mode
	if !o.config.TTS.StaticMode {
		return o.speakStreaming(ctx, guildID, text, ttsText)
	}
	return o.speakStatic(ctx, guildID, text, ttsText)
}

// speakStreaming generates audio with real-time streaming for lower latency.
// First audio starts playing in ~400-800ms instead of waiting for complete generation.
func (o *Orchestrator) speakStreaming(ctx context.Context, guildID, text, ttsText string) error {
	logger.Info("Using streaming TTS mode",
		zap.String("guild_id", guildID),
		zap.Int("text_len", len(text)))

	req := &TTSRequest{
		Text:    ttsText,
		VoiceID: o.config.TTS.DefaultVoice,
	}

	// Use a fresh context with timeout for TTS
	ttsCtx, ttsCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer ttsCancel()

	// Start streaming TTS
	audioStream, err := o.ttsClient.StreamStreaming(ttsCtx, req)
	if err != nil {
		logger.Error("TTS streaming failed, falling back to static",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to static mode
		return o.speakStatic(ctx, guildID, text, ttsText)
	}

	logger.Info("TTS streaming started, playing audio",
		zap.String("guild_id", guildID))

	// Play streaming audio
	if err := o.manager.PlayAudioStream(ctx, guildID, audioStream); err != nil {
		logger.Error("Failed to play streaming audio",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text
		o.sendTTSFallback(guildID, text)
		return fmt.Errorf("audio playback failed: %w", err)
	}

	o.lastTTSAudio = time.Now()
	return nil
}

// speakStatic generates complete audio before playing (legacy mode).
func (o *Orchestrator) speakStatic(ctx context.Context, guildID, text, ttsText string) error {
	logger.Info("Using static TTS mode",
		zap.String("guild_id", guildID),
		zap.Int("text_len", len(text)))

	req := &TTSRequest{
		Text:    ttsText,
		VoiceID: o.config.TTS.DefaultVoice,
	}

	// Use a fresh context with timeout for TTS
	ttsCtx, ttsCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer ttsCancel()

	audio, err := o.ttsClient.Generate(ttsCtx, req)
	if err != nil {
		logger.Error("TTS generation failed",
			zap.Error(err),
			zap.String("guild_id", guildID),
			zap.String("text", truncateText(text, 50)))

		// Fallback to text
		o.sendTTSFallback(guildID, text)
		return fmt.Errorf("TTS generation failed: %w", err)
	}

	logger.Info("TTS audio generated",
		zap.String("guild_id", guildID),
		zap.Int("audio_bytes", len(audio)),
		zap.Int("sample_rate", o.config.TTS.SampleRate))

	// Play the complete audio
	if err := o.manager.PlayAudio(ctx, guildID, audio, o.config.TTS.SampleRate); err != nil {
		logger.Error("Failed to play audio",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text
		o.sendTTSFallback(guildID, text)
		return fmt.Errorf("audio playback failed: %w", err)
	}

	o.lastTTSAudio = time.Now()
	return nil
}

// handleAudio is called when audio is received from a voice channel.
func (o *Orchestrator) handleAudio(guildID, userID string, audio []byte) {
	o.sessionsMu.RLock()
	conv, exists := o.sessions[guildID]
	o.sessionsMu.RUnlock()

	if !exists {
		return
	}

	conv.mu.Lock()
	defer conv.mu.Unlock()

	// Check if we're in idle timeout period
	if conv.StateManager.IsInIdle() {
		// Buffer audio during idle, don't process yet
		conv.StateManager.BufferAudio(audio)
		logger.Debug("Buffering audio during idle timeout",
			zap.String("guild_id", guildID),
			zap.Int("audio_bytes", len(audio)))
		return
	}

	// Only process audio when in listening state
	if conv.State != StateListening {
		logger.Debug("Ignoring audio - not in listening state",
			zap.String("guild_id", guildID),
			zap.String("state", conv.State.String()))
		return
	}

	// Track last speaker when Discord has resolved the SSRC mapping.
	if userID != "" {
		conv.LastSpeakerID = userID
	}

	// Add audio frame to buffer
	conv.AudioBuffer.AddFrame(audio)
	conv.LastActivity = time.Now()

	// Check if we should include idle buffered audio
	if idleBuffer := conv.StateManager.GetIdleBuffer(); len(idleBuffer) > 0 {
		// Prepend idle buffer to audio for context
		conv.AudioBuffer.PrependAudio(idleBuffer)
		logger.Debug("Prepended idle buffer to audio",
			zap.String("guild_id", guildID),
			zap.Int("idle_buffer_bytes", len(idleBuffer)))
	}

	// Get VAD settings from config
	silenceThresholdMs := o.config.Audio.VADSilenceMs
	if silenceThresholdMs == 0 {
		silenceThresholdMs = 700 // Default: 700ms silence = user stopped talking
	}
	maxDurationMs := o.config.Audio.VADMaxDurationMs
	if maxDurationMs == 0 {
		maxDurationMs = 10000 // Default: 10 seconds max
	}
	speechMinMs := o.config.Audio.VADSpeechMinMs
	if speechMinMs == 0 {
		speechMinMs = 300 // Default: 300ms minimum speech
	}

	bufferMs := conv.AudioBuffer.Duration()
	silenceMs := conv.AudioBuffer.SilenceDuration()
	speechMs := conv.AudioBuffer.SpeechDuration()
	hasSpeech := conv.AudioBuffer.HasSpeech()

	// Log VAD status when approaching silence threshold or when speech detected
	if silenceMs > 0 || speechMs > 100 {
		logger.Debug("Audio VAD status",
			zap.String("guild_id", guildID),
			zap.Int("buffer_ms", bufferMs),
			zap.Int("consecutive_silence_ms", silenceMs),
			zap.Int("speech_ms", speechMs),
			zap.Bool("has_speech", hasSpeech))
	}

	// Check for max duration (user talking too long)
	if bufferMs >= maxDurationMs && hasSpeech {
		logger.Info("Max duration reached, processing audio",
			zap.String("guild_id", guildID),
			zap.Int("buffer_ms", bufferMs),
			zap.Int("speech_ms", speechMs))
		go o.processBufferedAudio(guildID, o.resolveSpeakerID(conv, userID), conv)
		return
	}

	// Check for silence after speech (user stopped talking)
	// Only process if we have enough speech content
	if silenceMs >= silenceThresholdMs {
		if hasSpeech && speechMs >= speechMinMs {
			logger.Info("Silence detected after speech, processing audio",
				zap.String("guild_id", guildID),
				zap.Int("buffer_ms", bufferMs),
				zap.Int("speech_ms", speechMs),
				zap.Int("silence_ms", silenceMs))
			go o.processBufferedAudio(guildID, o.resolveSpeakerID(conv, userID), conv)
		} else if hasSpeech && speechMs < speechMinMs {
			// Very short speech - clear buffer, likely noise
			logger.Debug("Discarding short speech segment (likely noise)",
				zap.String("guild_id", guildID),
				zap.Int("speech_ms", speechMs),
				zap.Int("min_speech_ms", speechMinMs))
			conv.AudioBuffer.Clear()
		}
	}
}

// processBufferedAudio processes accumulated audio through STT and AI.
func (o *Orchestrator) processBufferedAudio(guildID, userID string, conv *VoiceConversation) {
	if !o.beginProcessing(conv) {
		logger.Debug("Skipping duplicate buffered audio processing",
			zap.String("guild_id", guildID))
		return
	}
	defer o.endProcessing(conv)

	conv.mu.Lock()
	// Get audio from buffer with silence trimmed (prevents Whisper hallucinations)
	audio := conv.AudioBuffer.GetTrimmedAudio()
	if userID == "" {
		userID = conv.LastSpeakerID
	}
	conv.mu.Unlock()

	if len(audio) == 0 {
		logger.Debug("No audio in buffer to process (after trimming silence)", zap.String("guild_id", guildID))
		return
	}

	logger.Info("Processing audio for STT",
		zap.String("guild_id", guildID),
		zap.Int("audio_bytes", len(audio)))

	// Check if STT client is available
	if o.sttClient == nil {
		logger.Error("STT client is nil - cannot transcribe",
			zap.String("guild_id", guildID))
		_ = o.manager.SendTextFallback(context.Background(), guildID, "Le service vocal n'est pas disponible.")
		return
	}

	// Convert from Discord format to STT format
	// Discord uses 48kHz stereo, STT typically expects 16kHz mono
	sttAudio, err := ConvertFromDiscordFormat(audio, 16000, 1)
	if err != nil {
		logger.Error("Failed to convert audio format",
			zap.String("guild_id", guildID),
			zap.Error(err))
		return
	}

	// Transcribe with timeout (60s total for STT + AI + TTS)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	text, err := o.sttClient.Transcribe(ctx, sttAudio)
	if err != nil {
		logger.Error("STT transcription failed",
			zap.String("guild_id", guildID),
			zap.Error(err))

		// Check if STT is unavailable
		if err == ErrSTTUnavailable {
			_ = o.manager.SendTextFallback(context.Background(), guildID,
				"Je n'arrive pas à comprendre ce que tu dis. Peux-tu répéter?")
		}
		return
	}

	// Skip empty transcriptions
	if text == "" || len(text) < 2 {
		logger.Info("Empty transcription, skipping",
			zap.String("guild_id", guildID))
		return
	}

	logger.Info("Voice transcription received",
		zap.String("guild_id", guildID),
		zap.String("user_id", userID),
		zap.String("text", text))

	// Process through AI
	if err := o.ProcessVoiceInput(ctx, userID, guildID, text); err != nil {
		logger.Error("Failed to process voice input",
			zap.Error(err),
			zap.String("guild_id", guildID))
	}
}

// handleJoin is called when the bot joins a voice channel.
func (o *Orchestrator) handleJoin(guildID, channelID string) {
	logger.Info("Voice orchestrator handling join",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID))
}

// handleLeave is called when the bot leaves a voice channel.
func (o *Orchestrator) handleLeave(guildID string) {
	o.sessionsMu.Lock()
	if conv, exists := o.sessions[guildID]; exists {
		if conv.CancelFunc != nil {
			conv.CancelFunc()
		}
		delete(o.sessions, guildID)
	}
	o.sessionsMu.Unlock()

	logger.Info("Voice orchestrator handling leave",
		zap.String("guild_id", guildID))
}

// transitionState handles state transitions.
func (o *Orchestrator) transitionState(guildID string, newState AgentState) {
	o.sessionsMu.Lock()
	if conv, exists := o.sessions[guildID]; exists {
		oldState := conv.State
		conv.State = newState
		o.manager.SetState(guildID, newState)

		logger.Debug("Voice state transition",
			zap.String("guild_id", guildID),
			zap.String("from", oldState.String()),
			zap.String("to", newState.String()))

		if o.onStateChange != nil {
			go o.onStateChange(guildID, oldState, newState)
		}
	}
	o.sessionsMu.Unlock()
}

// ensureTTSLoaded ensures the TTS model is loaded.
func (o *Orchestrator) ensureTTSLoaded(ctx context.Context) error {
	if o.ttsClient.IsModelLoaded() {
		return nil
	}

	// Check VRAM availability with a short timeout
	if o.config.VRAM.AutoUnload {
		vramCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		vram, err := o.ttsClient.GetVRAM(vramCtx)
		if err != nil {
			// Log warning but continue - VRAM check is not critical
			logger.Warn("VRAM check failed, attempting load anyway",
				zap.Error(err),
				zap.String("guild_id", "global"))
		} else if vram < int64(o.config.VRAM.MinFreeMB) {
			logger.Warn("Low VRAM, skipping TTS load",
				zap.Int64("free_mb", vram),
				zap.Int("min_required_mb", o.config.VRAM.MinFreeMB))
			return ErrInsufficientVRAM
		}
	}

	// Load model with a fresh context
	loadCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	return o.ttsClient.LoadModel(loadCtx)
}

// maybeUnloadTTS checks if TTS should be unloaded after inactivity.
func (o *Orchestrator) maybeUnloadTTS() {
	o.vramCheckMu.Lock()
	defer o.vramCheckMu.Unlock()

	// Check if any sessions are active
	o.sessionsMu.RLock()
	hasActiveSessions := len(o.sessions) > 0
	o.sessionsMu.RUnlock()

	if hasActiveSessions {
		return
	}

	// Check if enough time has passed since last TTS use
	timeout := time.Duration(o.config.VRAM.UnloadTimeoutSeconds) * time.Second
	if time.Since(o.lastTTSAudio) < timeout {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := o.ttsClient.UnloadModel(ctx); err != nil {
		logger.Warn("Failed to unload TTS model", zap.Error(err))
	}
}

// getDefaultPrompt generates the default system prompt.
func (o *Orchestrator) getDefaultPrompt(botNick string) string {
	tmpl, err := template.New("prompt").Parse(o.defaultPrompt)
	if err != nil {
		return fmt.Sprintf("yo, t'es %s, un pur bg du brainrot!", botNick)
	}

	var result strings.Builder
	data := map[string]string{"BotNick": botNick}
	if err := tmpl.Execute(&result, data); err != nil {
		return fmt.Sprintf("yo, t'es %s, un pur bg du brainrot!", botNick)
	}
	return result.String()
}

// GetManager returns the voice manager.
func (o *Orchestrator) GetManager() *Manager {
	return o.manager
}

// GetState returns the current state for a guild.
func (o *Orchestrator) GetState(guildID string) AgentState {
	o.sessionsMu.RLock()
	defer o.sessionsMu.RUnlock()
	if conv, exists := o.sessions[guildID]; exists {
		return conv.State
	}
	return StateIdle
}

// IsConnected returns true if connected to a voice channel.
func (o *Orchestrator) IsConnected(guildID string) bool {
	return o.manager.IsConnected(guildID)
}

// OnStateChange sets the callback for state changes.
func (o *Orchestrator) OnStateChange(callback func(guildID string, oldState, newState AgentState)) {
	o.onStateChange = callback
}

// silenceChecker periodically checks for silence based on wall-clock time.
// This is needed because Discord doesn't send audio packets during silence,
// so we can't rely on frame counting alone.
func (o *Orchestrator) silenceChecker(ctx context.Context, guildID string, conv *VoiceConversation) {
	// Get VAD settings from config
	silenceThresholdMs := o.config.Audio.VADSilenceMs
	if silenceThresholdMs == 0 {
		silenceThresholdMs = 700 // Default: 700ms silence = user stopped talking
	}
	speechMinMs := o.config.Audio.VADSpeechMinMs
	if speechMinMs == 0 {
		speechMinMs = 300 // Default: 300ms minimum speech
	}

	silenceThreshold := time.Duration(silenceThresholdMs) * time.Millisecond
	checkInterval := time.Duration(silenceThresholdMs/2) * time.Millisecond
	if checkInterval < 50*time.Millisecond {
		checkInterval = 50 * time.Millisecond
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			conv.mu.Lock()

			// Check if we're in listening state
			state := conv.State

			if state != StateListening {
				conv.mu.Unlock()
				continue
			}

			// If we're in idle period, check if we can exit idle
			if conv.StateManager.IsInIdle() {
				if !conv.StateManager.CanProcess() {
					// Still in idle timeout, skip processing
					conv.mu.Unlock()
					continue
				}
				// Idle timeout passed - exit idle and check for buffered audio
				idleBuffer := conv.StateManager.GetIdleBuffer() // This also exits idle state
				if len(idleBuffer) > 0 {
					// Check if idle buffer has speech
					conv.AudioBuffer.PrependAudio(idleBuffer)
					if conv.AudioBuffer.HasSpeech() && conv.AudioBuffer.SpeechDuration() >= speechMinMs {
						speakerID := conv.LastSpeakerID
						conv.mu.Unlock()
						logger.Info("Processing idle-buffered audio after idle timeout",
							zap.String("guild_id", guildID),
							zap.Int("buffer_ms", conv.AudioBuffer.Duration()),
							zap.Int("speech_ms", conv.AudioBuffer.SpeechDuration()))
						go o.processBufferedAudio(guildID, speakerID, conv)
					} else {
						// No speech in idle buffer, clear it
						conv.AudioBuffer.Clear()
						conv.mu.Unlock()
					}
				} else {
					conv.mu.Unlock()
				}
				continue
			}

			// Check time since last audio
			timeSinceLastAudio := conv.AudioBuffer.TimeSinceLastAudio()
			if timeSinceLastAudio == 0 {
				// No audio received yet
				conv.mu.Unlock()
				continue
			}

			// If silence threshold reached, process audio
			if timeSinceLastAudio >= silenceThreshold {
				hasSpeech := conv.AudioBuffer.HasSpeech()
				speechMs := conv.AudioBuffer.SpeechDuration()

				if hasSpeech && speechMs >= speechMinMs {
					bufferMs := conv.AudioBuffer.Duration()
					speakerID := conv.LastSpeakerID
					conv.mu.Unlock()

					logger.Info("Silence detected (time-based), processing audio",
						zap.String("guild_id", guildID),
						zap.Int("buffer_ms", bufferMs),
						zap.Int("speech_ms", speechMs),
						zap.Duration("silence_duration", timeSinceLastAudio))

					go o.processBufferedAudio(guildID, speakerID, conv)
				} else if hasSpeech && speechMs < speechMinMs {
					// Very short speech - clear buffer, likely noise
					logger.Debug("Discarding short speech segment (likely noise)",
						zap.String("guild_id", guildID),
						zap.Int("speech_ms", speechMs),
						zap.Int("min_speech_ms", speechMinMs))
					conv.AudioBuffer.Clear()
					conv.mu.Unlock()
				} else {
					// No speech - just clear
					conv.AudioBuffer.Clear()
					conv.mu.Unlock()
				}
			} else {
				conv.mu.Unlock()
			}
		}
	}
}

func (o *Orchestrator) resolveSpeakerID(conv *VoiceConversation, userID string) string {
	if userID != "" {
		return userID
	}
	return conv.LastSpeakerID
}

func (o *Orchestrator) beginProcessing(conv *VoiceConversation) bool {
	conv.mu.Lock()
	defer conv.mu.Unlock()
	if conv.processing {
		return false
	}
	conv.processing = true
	return true
}

func (o *Orchestrator) endProcessing(conv *VoiceConversation) {
	conv.mu.Lock()
	conv.processing = false
	conv.mu.Unlock()
}

func (o *Orchestrator) sendTTSFallback(guildID, text string) {
	if !o.config.TTS.FallbackToText {
		return
	}
	if err := o.manager.SendTextFallback(context.Background(), guildID, text); err != nil {
		logger.Warn("Failed to send TTS text fallback",
			zap.String("guild_id", guildID),
			zap.Error(err))
	}
}

func (o *Orchestrator) limitTTSText(text string) string {
	limit := o.config.TTS.MaxTextLength
	if limit <= 0 || len(text) <= limit {
		return text
	}

	trimmed := strings.TrimSpace(text[:limit])
	lastSpace := strings.LastIndex(trimmed, " ")
	if lastSpace >= limit/2 {
		trimmed = strings.TrimSpace(trimmed[:lastSpace])
	}

	logger.Debug("Truncated text for TTS request",
		zap.Int("original_len", len(text)),
		zap.Int("truncated_len", len(trimmed)),
		zap.Int("max_len", limit))

	return trimmed
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Close shuts down the orchestrator.
func (o *Orchestrator) Close() error {
	// Close all sessions
	o.sessionsMu.Lock()
	for guildID, conv := range o.sessions {
		if conv.CancelFunc != nil {
			conv.CancelFunc()
		}
		delete(o.sessions, guildID)
	}
	o.sessionsMu.Unlock()

	// Close manager
	if err := o.manager.Close(); err != nil {
		return err
	}

	// Close clients
	if o.sttClient != nil {
		_ = o.sttClient.Close()
	}
	if o.ttsClient != nil {
		_ = o.ttsClient.Close()
	}

	return nil
}
