package voice

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// Manager handles voice connections across multiple guilds.
type Manager struct {
	session   *discordgo.Session
	sessions  map[string]*VoiceSession // guildID -> session
	mu        sync.RWMutex
	onJoin    func(guildID, channelID string)
	onLeave   func(guildID string)
	onSpeak   func(guildID, userID string, audio []byte)
	onSilence func(guildID, userID string)

	// Track voice connections by guildID
	connections map[string]*discordgo.VoiceConnection
	connMu      sync.RWMutex

	// Opus decoders per guild
	opusDecoders map[string]*OpusDecodeSession
	opusMu       sync.Mutex

	// Opus encoder for TTS
	opusEncoder *OpusEncoder
	encoderMu   sync.Mutex
}

// NewManager creates a new voice manager.
func NewManager(session *discordgo.Session) *Manager {
	return &Manager{
		session:      session,
		sessions:     make(map[string]*VoiceSession),
		connections:  make(map[string]*discordgo.VoiceConnection),
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
		m.leaveVoiceLocked(guildID)
	}

	// Join the voice channel
	// mute=false, deaf=false - bot should be able to hear and speak
	vc, err := m.session.ChannelVoiceJoin(guildID, channelID, false, false)
	if err != nil {
		logger.Error("Failed to join voice channel",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID),
			zap.Error(err))
		return err
	}

	// Store the connection
	m.connMu.Lock()
	m.connections[guildID] = vc
	m.connMu.Unlock()

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
	go m.listenForAudio(vc, guildID)

	// Notify callback
	if m.onJoin != nil {
		go m.onJoin(guildID, channelID)
	}

	return nil
}

// LeaveVoice disconnects the bot from a voice channel.
func (m *Manager) LeaveVoice(guildID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leaveVoiceLocked(guildID)
}

// leaveVoiceLocked is the internal version that assumes the lock is held.
func (m *Manager) leaveVoiceLocked(guildID string) error {
	session, exists := m.sessions[guildID]
	if !exists {
		return ErrNotConnected
	}

	// Get and remove voice connection
	m.connMu.Lock()
	vc := m.connections[guildID]
	delete(m.connections, guildID)
	m.connMu.Unlock()

	if vc != nil {
		// Stop speaking if we were
		_ = vc.Speaking(false)

		// Disconnect
		err := vc.Disconnect()
		if err != nil {
			logger.Error("Error disconnecting from voice",
				zap.String("guild_id", guildID),
				zap.Error(err))
		}
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

// GetVoiceConnection returns the discordgo voice connection for a guild.
func (m *Manager) GetVoiceConnection(guildID string) (*discordgo.VoiceConnection, error) {
	m.connMu.RLock()
	defer m.connMu.RUnlock()
	vc := m.connections[guildID]
	if vc == nil {
		return nil, ErrNotConnected
	}
	return vc, nil
}

// listenForAudio handles incoming audio from a voice connection.
func (m *Manager) listenForAudio(vc *discordgo.VoiceConnection, guildID string) {
	logger.Info("Starting audio listener for guild", zap.String("guild_id", guildID))

	// Create Opus decoder for this session
	opusDecoder := NewOpusDecodeSession()
	m.opusMu.Lock()
	m.opusDecoders[guildID] = opusDecoder
	m.opusMu.Unlock()

	// Cleanup on exit
	defer func() {
		opusDecoder.Close()
		m.opusMu.Lock()
		delete(m.opusDecoders, guildID)
		m.opusMu.Unlock()
	}()

	// Add speaking handler for this connection
	speakingHandler := NewVoiceSpeakingHandler(m, guildID)
	vc.AddHandler(speakingHandler.Handle)

	packetCount := 0

	// Listen for opus packets
	for {
		select {
		case <-time.After(100 * time.Millisecond):
			// Check if connection is still valid
			if !vc.Ready {
				logger.Debug("Voice connection not ready, stopping listener", zap.String("guild_id", guildID))
				return
			}
		case packet, ok := <-vc.OpusRecv:
			if !ok {
				logger.Info("OpusRecv channel closed", zap.String("guild_id", guildID))
				return
			}

			packetCount++

			// Decode Opus to PCM
			pcmData, err := opusDecoder.DecodePacket(packet)
			if err != nil {
				logger.Error("Failed to decode Opus packet",
					zap.String("guild_id", guildID),
					zap.Error(err))
				continue
			}

			if len(pcmData) == 0 {
				continue
			}

			// Log every 50 packets to avoid spam
			if packetCount%50 == 0 {
				logger.Debug("Received and decoded audio packets",
					zap.String("guild_id", guildID),
					zap.Int("packet_count", packetCount),
					zap.Int("pcm_len", len(pcmData)))
			}

			// Handle the audio packet (now as PCM)
			if m.onSpeak != nil {
				m.onSpeak(guildID, "", pcmData)
			}
		}
	}
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

	vc, err := m.GetVoiceConnection(guildID)
	if err != nil {
		return err
	}

	// Update state
	m.SetState(guildID, StateSpeaking)

	// Signal speaking
	if err := vc.Speaking(true); err != nil {
		logger.Error("Failed to signal speaking", zap.Error(err))
		return err
	}
	defer vc.Speaking(false)

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

	// Send Opus frames with proper timing (20ms per frame)
	// Discord expects frames at 20ms intervals for smooth playback
	frameDuration := 20 * time.Millisecond
	for i, opusFrame := range opusFrames {
		select {
		case <-ctx.Done():
			m.SetState(guildID, StateListening)
			return ErrContextCanceled
		default:
			// Send with timeout to avoid blocking forever
			select {
			case vc.OpusSend <- opusFrame:
			case <-time.After(100 * time.Millisecond):
				logger.Warn("Timeout sending Opus frame", zap.String("guild_id", guildID))
			}

			// Wait for frame duration before sending next frame
			// Skip waiting after the last frame
			if i < len(opusFrames)-1 {
				time.Sleep(frameDuration)
			}
		}
	}

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
func (m *Manager) SendTextFallback(guildID, message string) error {
	m.mu.RLock()
	session, exists := m.sessions[guildID]
	m.mu.RUnlock()

	if !exists || session.TextChannelID == "" {
		return ErrNotConnected
	}

	_, err := m.session.ChannelMessageSend(session.TextChannelID, message)
	return err
}

// OnJoin sets the callback for when the bot joins a voice channel.
func (m *Manager) OnJoin(callback func(guildID, channelID string)) {
	m.onJoin = callback
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
		if err := m.leaveVoiceLocked(guildID); err != nil {
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
