package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
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
		logger.Info("Leaving current voice channel before joining new one",
			zap.String("guild_id", guildID),
			zap.String("old_channel", session.ChannelID),
			zap.String("new_channel", channelID))
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

	// Check if there's an existing connection in the voice manager and close it
	// This can happen if leaveVoice didn't fully clean up
	if existingConn := m.client.VoiceManager.GetConn(guildSnowflake); existingConn != nil {
		logger.Info("Found existing voice connection, closing it",
			zap.String("guild_id", guildID))
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		existingConn.Close(closeCtx)
		closeCancel()
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

	// Wait for gateway to be ready (DAVE key exchange must complete before receiving audio)
	readyTimeout := 30 * time.Second
	readyCtx, readyCancel := context.WithTimeout(context.Background(), readyTimeout)
	defer readyCancel()

	for {
		status := conn.Gateway().Status()
		if status == voice.StatusReady {
			break
		}
		select {
		case <-readyCtx.Done():
			logger.Error("Timeout waiting for voice gateway ready",
				zap.String("guild_id", guildID),
				zap.String("channel_id", channelID),
				zap.Int("status", int(status)))
			conn.Close(context.Background())
			return fmt.Errorf("timeout waiting for voice gateway ready")
		case <-time.After(100 * time.Millisecond):
			// Continue polling
		}
	}

	logger.Info("Voice gateway ready",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID))

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
		// Use a timeout context for closing to avoid blocking forever
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()

		logger.Info("Closing voice connection", zap.String("guild_id", guildID))
		conn.Close(closeCtx)
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
func (m *Manager) listenForAudio(conn voice.Conn, guildID string) {
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
		logger.Info("Audio listener stopped", zap.String("guild_id", guildID))
	}()

	packetCount := 0

	// Listen for UDP packets
	for {
		packet, err := conn.UDP().ReadPacket()
		if err != nil {
			if err == io.EOF {
				logger.Info("UDP connection closed", zap.String("guild_id", guildID))
				return
			}
			logger.Error("Error reading UDP packet",
				zap.String("guild_id", guildID),
				zap.Error(err))
			return
		}

		if packet == nil || len(packet.Opus) == 0 {
			continue
		}

		// Decode Opus to PCM
		pcmData, err := opusDecoder.DecodePacket(packet.Opus, packet.SSRC)
		if err != nil {
			logger.Error("Failed to decode Opus packet",
				zap.String("guild_id", guildID),
				zap.Uint32("ssrc", packet.SSRC),
				zap.Error(err))
			continue
		}

		if len(pcmData) == 0 {
			continue
		}

		packetCount++

		// Log every 50 packets to avoid spam
		if packetCount%50 == 0 {
			// Calculate audio level
			var maxSample int16
			for i := 0; i < len(pcmData)-1; i += 2 {
				sample := int16(binary.LittleEndian.Uint16(pcmData[i:]))
				if sample > maxSample {
					maxSample = sample
				}
			}
			logger.Info("Received audio packet",
				zap.String("guild_id", guildID),
				zap.Int("packet_count", packetCount),
				zap.Int("pcm_len", len(pcmData)),
				zap.Uint32("ssrc", packet.SSRC),
				zap.Int16("max_amplitude", maxSample))
		}

		// Handle the audio packet (now as PCM)
		if m.onSpeak != nil {
			m.onSpeak(guildID, "", pcmData)
		}
	}
}

// PlayAudio sends audio data to the voice channel.
// It automatically handles WAV format by parsing the header and converting to Discord's expected format.
// Audio is encoded to Opus before sending to Discord.
func (m *Manager) PlayAudio(ctx context.Context, guildID string, audio []byte, sampleRate int) error {
	logger.Info("PlayAudio: ENTERING",
		zap.String("guild_id", guildID),
		zap.Int("audio_bytes", len(audio)),
		zap.Int("sample_rate", sampleRate))

	// Check context before starting
	select {
	case <-ctx.Done():
		logger.Warn("PlayAudio: context already cancelled", zap.String("guild_id", guildID), zap.Error(ctx.Err()))
		return ctx.Err()
	default:
	}

	m.mu.RLock()
	session, exists := m.sessions[guildID]
	m.mu.RUnlock()

	if !exists {
		logger.Error("PlayAudio: no session found", zap.String("guild_id", guildID))
		return ErrNotConnected
	}
	logger.Info("PlayAudio: session found", zap.String("guild_id", guildID), zap.String("channel_id", session.ChannelID))

	logger.Info("PlayAudio: getting voice connection", zap.String("guild_id", guildID))
	conn, err := m.GetVoiceConnection(guildID)
	if err != nil {
		logger.Error("PlayAudio: failed to get connection", zap.Error(err))
		return err
	}

	// Update state
	m.SetState(guildID, StateSpeaking)

	// Signal speaking using disgo API
	logger.Info("PlayAudio: setting speaking flag", zap.String("guild_id", guildID))
	if err := conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		logger.Error("Failed to signal speaking", zap.Error(err))
		return err
	}
	defer conn.SetSpeaking(context.Background(), 0) // Stop speaking

	// Process audio: handle WAV format and convert to Discord format
	logger.Info("PlayAudio: processing audio for Discord", zap.String("guild_id", guildID))
	processedAudio, err := m.processAudioForDiscord(audio, sampleRate)
	if err != nil {
		logger.Error("Failed to process audio", zap.Error(err))
		return err
	}

	logger.Info("Processing audio for Discord",
		zap.String("guild_id", guildID),
		zap.Int("pcm_bytes", len(processedAudio)))

	// Get or create Opus encoder
	logger.Info("PlayAudio: getting Opus encoder", zap.String("guild_id", guildID))
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
	logger.Info("PlayAudio: encoding to Opus", zap.String("guild_id", guildID))
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
	writer := conn.UDP()

	for i, opusFrame := range opusFrames {
		select {
		case <-ctx.Done():
			m.SetState(guildID, StateListening)
			return ErrContextCanceled
		default:
			// Write raw Opus frame to UDP
			if _, err := writer.Write(opusFrame); err != nil {
				logger.Error("Failed to write Opus frame",
					zap.String("guild_id", guildID),
					zap.Int("frame", i),
					zap.Error(err))
				continue
			}

			// Wait for frame duration before sending next frame
			// Skip waiting after the last frame
			if i < len(opusFrames)-1 {
				time.Sleep(frameDuration)
			}
		}
	}

	logger.Info("Finished sending audio",
		zap.String("guild_id", guildID),
		zap.Int("frames_sent", len(opusFrames)))

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

// PlayAudioStream streams audio chunks from a channel and plays them in real-time.
// This is more efficient than PlayAudio for longer responses as it starts
// playback immediately instead of waiting for complete generation.
// The audio stream should be raw PCM data at the specified sample rate.
func (m *Manager) PlayAudioStream(ctx context.Context, guildID string, audioStream <-chan StreamChunk) error {
	logger.Info("PlayAudioStream: starting",
		zap.String("guild_id", guildID))

	// Check context before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

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

	// Signal speaking
	if err := conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		return err
	}
	defer conn.SetSpeaking(context.Background(), 0)

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

	// Pre-buffer for smoother playback
	// Buffer first 500ms (25 frames) before starting to play
	// This prevents audio chopping at the start
	preBufferFrames := 50 // 500ms / 20ms per frame = 25 frames
	var preBuffer [][]byte

	// Stream processing
	writer := conn.UDP()
	frameDuration := 20 * time.Millisecond
	totalFrames := 0
	preBufferCount := 0

	for chunk := range audioStream {
		if chunk.Err != nil {
			logger.Error("Stream error",
				zap.String("guild_id", guildID),
				zap.Error(chunk.Err))
			break
		}

		// Convert PCM chunk to Discord format (48kHz stereo)
		converted, err := ConvertToDiscordFormat(chunk.Data, chunk.SampleRate, 1) // assuming mono
		if err != nil {
			logger.Error("Failed to convert audio",
				zap.String("guild_id", guildID),
				zap.Error(err))
			continue
		}

		// Encode to Opus
		opusFrames, err := encoder.Encode(converted)
		if err != nil {
			logger.Error("Failed to encode Opus",
				zap.String("guild_id", guildID),
				zap.Error(err))
			continue
		}

		// Pre-buffer first few frames for smoother start
		if preBufferCount < preBufferFrames {
			preBuffer = append(preBuffer, opusFrames...)
			preBufferCount += len(opusFrames)
			continue
		}

		// Send pre-buffered frames first (only once)
		if len(preBuffer) > 0 {
			for i, frame := range preBuffer {
				select {
				case <-ctx.Done():
					m.SetState(guildID, StateListening)
					return ctx.Err()
				default:
					if _, err := writer.Write(frame); err != nil {
						logger.Error("Failed to write pre-buffer frame",
							zap.String("guild_id", guildID),
							zap.Error(err))
					}
					if i < len(preBuffer)-1 {
						time.Sleep(frameDuration)
					}
				}
			}
			preBuffer = nil // Clear after sending
			logger.Debug("Pre-buffer sent",
				zap.String("guild_id", guildID),
				zap.Int("frames", preBufferCount))
		}

		// Send frames with proper timing
		for i, frame := range opusFrames {
			select {
			case <-ctx.Done():
				m.SetState(guildID, StateListening)
				return ctx.Err()
			default:
				if _, err := writer.Write(frame); err != nil {
					logger.Error("Failed to write frame",
						zap.String("guild_id", guildID),
						zap.Error(err))
					continue
				}
				// Wait 20ms between frames (except last)
				if i < len(opusFrames)-1 {
					time.Sleep(frameDuration)
				}
			}
		}
		totalFrames += len(opusFrames)
	}

	// Send any remaining pre-buffer if stream ended early
	if len(preBuffer) > 0 && preBufferCount > 0 {
		for i, frame := range preBuffer {
			select {
			case <-ctx.Done():
				m.SetState(guildID, StateListening)
				return ctx.Err()
			default:
				if _, err := writer.Write(frame); err != nil {
					logger.Error("Failed to write remaining frame",
						zap.String("guild_id", guildID),
						zap.Error(err))
				}
				if i < len(preBuffer)-1 {
					time.Sleep(frameDuration)
				}
			}
		}
	}

	logger.Info("PlayAudioStream: finished",
		zap.String("guild_id", guildID),
		zap.Int("total_frames", totalFrames))

	// Return to listening state
	m.SetState(guildID, StateListening)
	return nil
}

// SendTextFallback sends a text message to the voice session's text channel.
func (m *Manager) SendTextFallback(ctx context.Context, guildID, message string) error {
	m.mu.RLock()
	session, exists := m.sessions[guildID]
	m.mu.RUnlock()

	if !exists || session.TextChannelID == "" {
		return ErrNotConnected
	}

	channelID, err := snowflake.Parse(session.TextChannelID)
	if err != nil {
		return fmt.Errorf("invalid channel ID: %w", err)
	}

	_, err = m.client.Rest.CreateMessage(channelID, discord.MessageCreate{
		Content: message,
	}, rest.WithCtx(ctx))
	if err != nil {
		logger.Error("Failed to send text fallback",
			zap.String("guild_id", guildID),
			zap.String("channel_id", session.TextChannelID),
			zap.Error(err))
		return err
	}

	logger.Debug("Sent text fallback",
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
