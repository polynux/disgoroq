package voice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
)

// Orchestrator manages the voice conversation flow.
// It coordinates between STT (speech-to-text), AI service, and TTS (text-to-speech).
type Orchestrator struct {
	manager        *Manager
	sttClient      STTClient
	ttsClient      TTSClient
	aiService      *ai.Service
	memoryService  memory.Service
	contextBuilder *ai.ContextBuilder
	session        *discordgo.Session
	repo           Repository
	defaultPrompt  string
	config         config.VoiceConfig

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
	GetTemperature(ctx context.Context, guildID string) float32
	GetPrompt(ctx context.Context, guildID string) (string, bool)
}

// VoiceConversation represents an active voice conversation in a guild.
type VoiceConversation struct {
	GuildID       string
	TextChannelID string
	State         AgentState
	AudioBuffer   *AudioBufferManager
	LastActivity  time.Time
	CancelFunc    context.CancelFunc
}

// OrchestratorConfig contains configuration for the orchestrator.
type OrchestratorConfig struct {
	STTClient     STTClient
	TTSClient     TTSClient
	AIService     *ai.Service
	MemoryService memory.Service
	Session       *discordgo.Session
	Repository    Repository
	DefaultPrompt string
	VoiceConfig   config.VoiceConfig
}

// NewOrchestrator creates a new voice orchestrator.
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	o := &Orchestrator{
		manager:        NewManager(cfg.Session),
		sttClient:      cfg.STTClient,
		ttsClient:      cfg.TTSClient,
		aiService:      cfg.AIService,
		memoryService:  cfg.MemoryService,
		contextBuilder: ai.NewContextBuilder(cfg.Session, cfg.AIService),
		session:        cfg.Session,
		repo:           cfg.Repository,
		defaultPrompt:  cfg.DefaultPrompt,
		config:         cfg.VoiceConfig,
		sessions:       make(map[string]*VoiceConversation),
	}

	// Set up audio callback
	o.manager.OnSpeak(o.handleAudio)
	o.manager.OnJoin(o.handleJoin)
	o.manager.OnLeave(o.handleLeave)

	return o
}

// JoinVoice connects to a voice channel and starts listening.
func (o *Orchestrator) JoinVoice(ctx context.Context, guildID, channelID, textChannelID string) error {
	// Check if already connected
	if o.manager.IsConnected(guildID) {
		return ErrAlreadyConnected
	}

	// Join the voice channel
	if err := o.manager.JoinVoice(ctx, guildID, channelID, textChannelID); err != nil {
		return err
	}

	// Create conversation session
	conv := &VoiceConversation{
		GuildID:       guildID,
		TextChannelID: textChannelID,
		State:         StateListening,
		AudioBuffer: NewAudioBufferManager(
			o.config.Audio.SampleRate,
			o.config.Audio.Channels,
			20, // 20ms frames
		),
		LastActivity: time.Now(),
	}

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

	return o.manager.LeaveVoice(guildID)
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
	botMember, err := o.session.GuildMember(guildID, o.session.State.User.ID)
	if err != nil {
		logger.Warn("Failed to get bot member", zap.Error(err))
	}

	botNick := ""
	if botMember != nil {
		botNick = botMember.Nick
	}

	// Build the system prompt
	instructions := o.getDefaultPrompt(botNick)
	if prompt, ok := o.repo.GetPrompt(ctx, guildID); ok {
		instructions = prompt
	}

	// Add voice context
	instructions += "\n\n[Voice mode: Tu es en conversation vocale. Réponds de manière concise et naturelle, comme dans une vraie conversation. Évite les réponses trop longues.]"

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
			instructions += memoryContext
		}
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

	// Build message context
	message := ai.Message{
		Role:     "user",
		Content:  text,
		AuthorID: userID,
	}

	// Call AI service
	response, err := o.aiService.Chat(ctx, &ai.ChatRequest{
		SystemPrompt: instructions,
		Messages:     []ai.Message{message},
		Temperature:  temperature,
		MaxTokens:    150, // Shorter responses for voice
	})

	if err != nil {
		logger.Error("AI chat failed in voice mode",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text
		fallbackMsg := "Désolé, j'ai eu un problème technique..."
		return o.manager.SendTextFallback(guildID, fallbackMsg)
	}

	if response.Content == "" {
		return o.manager.SendTextFallback(guildID, "Euh... je n'ai rien à dire...")
	}

	// Buffer response for memory
	if o.memoryService != nil {
		go func() {
			if err := o.memoryService.BufferMessage(context.Background(), o.session.State.User.ID, guildID, response.Content); err != nil {
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
// Uses static generation: generates the entire response first, then plays it.
func (o *Orchestrator) Speak(ctx context.Context, guildID, text string) error {
	logger.Info("Speak() called",
		zap.String("guild_id", guildID),
		zap.Int("text_len", len(text)))

	// Update state to speaking
	o.transitionState(guildID, StateSpeaking)
	defer o.transitionState(guildID, StateListening)

	// Ensure TTS model is loaded
	logger.Info("Checking TTS model availability", zap.String("guild_id", guildID))
	if err := o.ensureTTSLoaded(ctx); err != nil {
		logger.Error("Failed to load TTS model",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text
		_ = o.manager.SendTextFallback(guildID, text)
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

	// Generate audio for entire text at once (static mode)
	req := &TTSRequest{
		Text: text,
	}

	logger.Info("Generating TTS audio",
		zap.String("guild_id", guildID),
		zap.Int("text_len", len(text)))

	// Use a fresh context with timeout for TTS to avoid deadline exceeded from parent context
	ttsCtx, ttsCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer ttsCancel()

	audio, err := o.ttsClient.Generate(ttsCtx, req)
	if err != nil {
		logger.Error("TTS generation failed",
			zap.Error(err),
			zap.String("guild_id", guildID),
			zap.String("text", truncateText(text, 50)))

		// Fallback to text
		_ = o.manager.SendTextFallback(guildID, text)
		return fmt.Errorf("TTS generation failed: %w", err)
	}

	logger.Info("TTS audio generated",
		zap.String("guild_id", guildID),
		zap.Int("audio_bytes", len(audio)),
		zap.Int("sample_rate", o.config.TTS.SampleRate))

	// Play the complete audio
	logger.Info("Calling PlayAudio", zap.String("guild_id", guildID))
	if err := o.manager.PlayAudio(ctx, guildID, audio, o.config.TTS.SampleRate); err != nil {
		logger.Error("Failed to play audio",
			zap.Error(err),
			zap.String("guild_id", guildID))

		// Fallback to text
		_ = o.manager.SendTextFallback(guildID, text)
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

	// Only process audio when in listening state
	if conv.State != StateListening {
		logger.Debug("Ignoring audio - not in listening state",
			zap.String("guild_id", guildID),
			zap.String("state", conv.State.String()))
		return
	}

	// Log audio reception (every 50 frames to avoid spam)
	conv.AudioBuffer.AddFrame(audio)
	conv.LastActivity = time.Now()

	// Check if we have enough silence to process
	silenceThreshold := time.Duration(o.config.Audio.BufferMs) * time.Millisecond
	silenceDuration := conv.AudioBuffer.SilenceDuration()
	bufferDuration := conv.AudioBuffer.Duration()

	logger.Debug("Audio buffer status",
		zap.String("guild_id", guildID),
		zap.Int("buffer_ms", bufferDuration),
		zap.Int("silence_ms", silenceDuration),
		zap.Int("threshold_ms", int(silenceThreshold.Milliseconds())),
		zap.Bool("has_speech", conv.AudioBuffer.HasSpeech()))

	if silenceDuration >= int(silenceThreshold.Milliseconds()) {
		if conv.AudioBuffer.HasSpeech() {
			logger.Info("Processing buffered audio",
				zap.String("guild_id", guildID),
				zap.Int("buffer_ms", bufferDuration))

			// Process the buffered audio
			go o.processBufferedAudio(guildID, userID, conv)
		}
	}
}

// processBufferedAudio processes accumulated audio through STT and AI.
func (o *Orchestrator) processBufferedAudio(guildID, userID string, conv *VoiceConversation) {
	// Get audio from buffer
	audio := conv.AudioBuffer.GetAudio()
	if len(audio) == 0 {
		logger.Debug("No audio in buffer to process", zap.String("guild_id", guildID))
		return
	}

	logger.Info("Processing audio for STT",
		zap.String("guild_id", guildID),
		zap.Int("audio_bytes", len(audio)),
		zap.Int("buffer_ms", conv.AudioBuffer.Duration()))

	// Check if STT client is available
	if o.sttClient == nil {
		logger.Error("STT client is nil - cannot transcribe",
			zap.String("guild_id", guildID))
		_ = o.manager.SendTextFallback(guildID, "Le service vocal n'est pas disponible.")
		return
	}

	logger.Info("Converting audio format",
		zap.String("guild_id", guildID),
		zap.Int("input_bytes", len(audio)))

	// Convert from Discord format to STT format
	// Discord uses 48kHz stereo, STT typically expects 16kHz mono
	sttAudio, err := ConvertFromDiscordFormat(audio, 16000, 1)
	if err != nil {
		logger.Error("Failed to convert audio format",
			zap.String("guild_id", guildID),
			zap.Error(err))
		return
	}

	logger.Info("Sending audio to STT",
		zap.String("guild_id", guildID),
		zap.Int("stt_bytes", len(sttAudio)))

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
			_ = o.manager.SendTextFallback(guildID,
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
