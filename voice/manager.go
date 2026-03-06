package voice

import (
	"context"
	"fmt"
	"sync"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// Manager handles voice connections across multiple guilds.
type Manager struct {
	client    *bot.Client
	sessions  map[string]*VoiceSession // guildID -> session
	mu        sync.RWMutex
	onJoin    func(guildID, channelID string)
	onLeave   func(guildID string)
	onSpeak   func(guildID, userID string, audio []byte)
	onSilence func(guildID, userID string)

	// Track voice connections by guildID using snowflake.ID
	// connections map[snowflake.ID]voice.Conn - managed by client.VoiceManager

	// Opus decoders per guild
	opusDecoders map[string]*OpusDecodeSession
	opusMu       sync.Mutex

	// Opus encoder for TTS
	opusEncoder *OpusEncoder
	encoderMu   sync.Mutex
}

// NewManager creates a new voice manager.
func NewManager(client *bot.Client) *Manager {
	return &Manager{
		client:       client,
		sessions:     make(map[string]*VoiceSession),
		opusDecoders: make(map[string]*OpusDecodeSession),
	}
}

// JoinVoice connects the bot to a voice channel in a guild.
func (m *Manager) JoinVoice(ctx context.Context, guildID, channelID, textChannelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already connected to this guild
	if session, exists := m.sessions[guildID]; exists {
		if session.ChannelID == channelID {
			return ErrAlreadyConnected
		}
		// Leave current channel first
		m.leaveVoiceLocked(ctx, guildID)
	}

	// Convert string IDs to snowflake
	guildSnowflake, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("invalid guild ID: %w", err)
	}
	channelSnowflake, err := snowflake.Parse(channelID)
	if err != nil {
		return fmt.Errorf("invalid channel ID: %w", err)
	}

	// Create voice connection using disgo's voice manager
	conn := m.client.VoiceManager.CreateConn(guildSnowflake)

	// Open voice connection (selfMute=false, selfDeaf=false)
	if err := conn.Open(ctx, channelSnowflake, false, false); err != nil {
		logger.Error("Failed to join voice channel",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID),
			zap.Error(err))
		return err
	}

	// Create new voice session
	session := &VoiceSession{
		GuildID:       guildID,
		ChannelID:     channelID,
		TextChannelID: textChannelID,
		State:         StateIdle,
		AudioBuffer:   NewAudioBuffer(8192, 48000, 2), // 48kHz, stereo
	}
	m.sessions[guildID] = session

	logger.Info("Joined voice channel",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID))

	// Start listening for audio
	go m.listenForAudio(conn, guildID)

	// Notify callback
	if m.onJoin != nil {
		go m.onJoin(guildID, channelID)
	}

	return nil
}

// LeaveVoice disconnects the bot from a voice channel.
func (m *Manager) LeaveVoice(ctx context.Context, guildID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leaveVoiceLocked(ctx, guildID)
}

// leaveVoiceLocked is the internal version that assumes the lock is held.
func (m *Manager) leaveVoiceLocked(ctx context.Context, guildID string) error {
	session, exists := m.sessions[guildID]
	if !exists {
		return ErrNotConnected
	}

	// Get voice connection from disgo manager
	guildSnowflake, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("invalid guild ID: %w", err)
	}

	conn := m.client.VoiceManager.GetConn(guildSnowflake)
	if conn != nil {
		// Close the connection
		conn.Close(ctx)
	}

	// Clear session
	delete(m.sessions, guildID)

	logger.Info("Left voice channel",
		zap.String("guild_id", guildID),
		zap.String("channel_id", session.ChannelID))

	// Notify callback
	if m.onLeave != nil {
		go m.onLeave(guildID)
	}

	return nil
}

// GetSession returns the voice session for a guild.
func (m *Manager) GetSession(guildID string) (*VoiceSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, exists := m.sessions[guildID]
	return session, exists
}

// GetState returns the current state for a guild.
func (m *Manager) GetState(guildID string) AgentState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if session, exists := m.sessions[guildID]; exists {
		return session.State
	}
	return StateIdle
}

// SetState updates the state for a guild's voice session.
func (m *Manager) SetState(guildID string, state AgentState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, exists := m.sessions[guildID]; exists {
		oldState := session.State
		session.State = state
		logger.Debug("Voice state transition",
			zap.String("guild_id", guildID),
			zap.String("from", oldState.String()),
			zap.String("to", state.String()))
	}
}

// IsConnected returns true if connected to a voice channel in the guild.
func (m *Manager) IsConnected(guildID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.sessions[guildID]
	return exists
}

// GetVoiceConnection returns the disgo voice connection for a guild.
func (m *Manager) GetVoiceConnection(guildID string) (voice.Conn, error) {
	guildSnowflake, err := snowflake.Parse(guildID)
	if err != nil {
		return nil, fmt.Errorf("invalid guild ID: %w", err)
	}

	conn := m.client.VoiceManager.GetConn(guildSnowflake)
	if conn == nil {
		return nil, ErrNotConnected
	}
	return conn, nil
}

// listenForAudio handles incoming audio from a voice connection.
// TODO: Implement disgo OpusFrameReceiver
func (m *Manager) listenForAudio(conn voice.Conn, guildID string) {
	logger.Info("Starting audio listener for guild", zap.String("guild_id", guildID))
	// TODO: Implement audio reception using disgo's OpusFrameReceiver
	// This will require:
	// 1. Implementing OpusFrameReceiver interface
	// 2. Setting it via conn.SetOpusFrameReceiver()
	// 3. Buffering incoming frames
	// 4. Decoding Opus to PCM
	// 5. Calling onSpeak callbacks
}

// PlayAudio sends audio data to the voice channel.
// It automatically handles WAV format by parsing the header and converting to Discord's expected format.
// Audio is encoded to Opus before sending to Discord.
func (m *Manager) PlayAudio(ctx context.Context, guildID string, audio []byte, sampleRate int) error {
	m.mu.RLock()
	_, exists := m.sessions[guildID]
	m.mu.RUnlock()

	if !exists {
		return ErrNotConnected
	}

	conn, err := m.GetVoiceConnection(guildID)
	if err != nil {
		return err
	}

	// Update state
	m.SetState(guildID, StateSpeaking)

	// Signal speaking using disgo API
	if err := conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		logger.Error("Failed to signal speaking", zap.Error(err))
		return err
	}
	defer conn.SetSpeaking(context.Background(), 0) // Stop speaking

	// Process audio: handle WAV format and convert to Discord format
	processedAudio, err := m.processAudioForDiscord(audio, sampleRate)
	if err != nil {
		logger.Error("Failed to process audio", zap.Error(err))
		return err
	}

	logger.Info("Processing audio for Discord",
		zap.String("guild_id", guildID),
		zap.Int("pcm_bytes", len(processedAudio)))

	// Get or create Opus encoder
	m.encoderMu.Lock()
	if m.opusEncoder == nil {
		m.opusEncoder, err = NewOpusEncoder()
		if err != nil {
			m.encoderMu.Unlock()
			return fmt.Errorf("failed to create Opus encoder: %w", err)
		}
	}
	encoder := m.opusEncoder
	m.encoderMu.Unlock()

	// Encode PCM to Opus frames
	opusFrames, err := encoder.Encode(processedAudio)
	if err != nil {
		return fmt.Errorf("failed to encode Opus: %w", err)
	}

	logger.Info("Encoded Opus frames",
		zap.String("guild_id", guildID),
		zap.Int("frame_count", len(opusFrames)))

	// TODO: Implement audio sending using disgo's OpusFrameProvider
	// Need to create OpusFrameProvider implementation and pass it to conn.SetOpusFrameProvider()
	// For now, log a warning that this needs implementation
	logger.Warn("PlayAudio not fully implemented for disgo - audio will not be sent",
		zap.String("guild_id", guildID),
		zap.Int("frames", len(opusFrames)))

	// Return to listening state
	m.SetState(guildID, StateListening)
	return nil
}

// processAudioForDiscord converts audio to Discord's expected format (48kHz stereo PCM).
func (m *Manager) processAudioForDiscord(audio []byte, sampleRate int) ([]byte, error) {
	inputAudio := audio
	inputChannels := 1 // Assume mono by default

	// Check if audio is WAV format
	if IsWAV(audio) {
		pcmData, info, err := ParseWAV(audio)
		if err != nil {
			return nil, fmt.Errorf("failed to parse WAV: %w", err)
		}

		logger.Info("Parsed WAV audio",
			zap.Int("original_bytes", len(audio)),
			zap.Int("sample_rate", info.SampleRate),
			zap.Int("channels", info.Channels),
			zap.Int("bits", info.BitsPerSample),
			zap.Int("pcm_size", len(pcmData)))

		inputAudio = pcmData
		inputChannels = info.Channels
		sampleRate = info.SampleRate
	}

	// Convert to Discord format (48kHz stereo)
	converted, err := ConvertToDiscordFormat(inputAudio, sampleRate, inputChannels)
	if err != nil {
		return nil, fmt.Errorf("failed to convert audio format: %w", err)
	}

	logger.Info("Converted audio for Discord",
		zap.Int("input_bytes", len(inputAudio)),
		zap.Int("input_sample_rate", sampleRate),
		zap.Int("input_channels", inputChannels),
		zap.Int("output_bytes", len(converted)),
		zap.Int("output_sample_rate", 48000),
		zap.Int("output_channels", 2))

	return converted, nil
}

// SendTextFallback sends a text message to the voice session's text channel.
func (m *Manager) SendTextFallback(ctx context.Context, guildID, message string) error {
	m.mu.RLock()
	session, exists := m.sessions[guildID]
	m.mu.RUnlock()

	if !exists || session.TextChannelID == "" {
		return ErrNotConnected
	}

	// TODO: Use disgo client to send message
	// _, err := m.client.Rest.CreateMessage(ctx, channelID, discord.MessageCreate{...})
	logger.Warn("SendTextFallback not fully implemented for disgo",
		zap.String("guild_id", guildID),
		zap.String("channel_id", session.TextChannelID))
	return nil
}

// OnLeave sets the callback for when the bot leaves a voice channel.
func (m *Manager) OnLeave(callback func(guildID string)) {
	m.onLeave = callback
}

// OnSpeak sets the callback for when audio is received.
func (m *Manager) OnSpeak(callback func(guildID, userID string, audio []byte)) {
	m.onSpeak = callback
}

// OnSilence sets the callback for when silence is detected.
func (m *Manager) OnSilence(callback func(guildID, userID string)) {
	m.onSilence = callback
}

// Close disconnects from all voice channels.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for guildID := range m.sessions {
		if err := m.leaveVoiceLocked(context.Background(), guildID); err != nil {
			lastErr = err
		}
	}

	return lastErr
}
// GetAllSessions returns all active voice sessions.
func (m *Manager) GetAllSessions() map[string]*VoiceSession {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make(map[string]*VoiceSession, len(m.sessions))
	for k, v := range m.sessions {
		result[k] = v
	}
	return result
}
