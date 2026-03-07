package handlers

import (
	"context"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/voice"
)

// VoiceHandler handles Discord voice state events.
type VoiceHandler struct {
	orchestrator *voice.Orchestrator
	repo         *database.Repository
	client       *bot.Client
}

// NewVoiceHandler creates a new voice handler.
func NewVoiceHandler(orchestrator *voice.Orchestrator, repo *database.Repository, client *bot.Client) *VoiceHandler {
	return &VoiceHandler{
		orchestrator: orchestrator,
		repo:         repo,
		client:       client,
	}
}

// HandleVoiceStateUpdate handles voice state update events from Discord.
// This is used for auto-join functionality and DAVE encryption tracking.
func (h *VoiceHandler) HandleVoiceStateUpdate(e *events.GuildVoiceStateUpdate) {
	// Forward voice state update to active voice connection for DAVE encryption
	// This must happen before any other logic to ensure DAVE can track users
	h.forwardToVoiceConnection(e)

	// Skip if orchestrator or repo is not initialized
	if h.orchestrator == nil || h.repo == nil {
		return
	}

	guildID := e.VoiceState.GuildID.String()
	userID := e.VoiceState.UserID.String()

	// Skip events for the bot itself
	if userID == h.client.ID().String() {
		h.handleBotVoiceStateUpdate(e)
		return
	}

	// Create a context with timeout for database operations
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dbCancel()

	// Check if auto-join is enabled for this guild
	autoJoin := h.repo.GetVoiceAutoJoin(dbCtx, guildID)
	if !autoJoin {
		return
	}

	// Get the configured auto-join channel
	autoJoinChannel, hasChannel := h.repo.GetVoiceAutoJoinChannel(dbCtx, guildID)
	if !hasChannel || autoJoinChannel == "" {
		return
	}

	// Check if this is a join event to the auto-join channel
	if e.VoiceState.ChannelID != nil && e.VoiceState.ChannelID.String() == autoJoinChannel {
		// Check if we're not already connected
		if !h.orchestrator.IsConnected(guildID) {
			// Check if voice is enabled
			if !h.repo.GetVoiceEnabled(dbCtx, guildID) {
				return
			}

			logger.Info("Auto-joining voice channel",
				zap.String("guild_id", guildID),
				zap.String("channel_id", autoJoinChannel),
				zap.String("trigger_user_id", userID))

			// Find a text channel for fallback messages
			textChannelID := h.findDefaultTextChannel(e.VoiceState.GuildID)

			// Use a longer context for voice join (DAVE handshake can take 30+ seconds)
			joinCtx, joinCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer joinCancel()

			// Join the voice channel
			if err := h.orchestrator.JoinVoice(joinCtx, guildID, autoJoinChannel, textChannelID); err != nil {
				logger.Error("Failed to auto-join voice channel",
					zap.String("guild_id", guildID),
					zap.String("channel_id", autoJoinChannel),
					zap.Error(err))
			}
		}
	} else if e.VoiceState.ChannelID == nil {
		// User left a voice channel - check if we should leave too
		h.maybeLeaveEmptyChannel(e)
	}
}

// forwardToVoiceConnection forwards voice state updates to the voice manager.
// The voice manager handles routing to active connections for DAVE encryption.
// This is required for DAVE to track users joining/leaving the voice channel.
func (h *VoiceHandler) forwardToVoiceConnection(e *events.GuildVoiceStateUpdate) {
	// Forward to the voice manager which handles routing to active connections
	// The voice manager will check if there's an active connection for this guild
	// and forward the event to DAVE for encryption tracking
	evt := gateway.EventVoiceStateUpdate{
		VoiceState: e.GenericGuildVoiceState.VoiceState,
		Member:     e.GenericGuildVoiceState.Member,
	}

	// Use the bot's voice manager which handles connection lifecycle properly
	h.client.VoiceManager.HandleVoiceStateUpdate(evt)

	logger.Debug("Forwarded voice state update to voice manager",
		zap.String("guild_id", e.VoiceState.GuildID.String()),
		zap.String("user_id", e.VoiceState.UserID.String()),
		zap.String("channel_id", channelIDStr(e.VoiceState.ChannelID)))
}

// channelIDStr safely converts a channel ID pointer to string.
func channelIDStr(id *snowflake.ID) string {
	if id == nil {
		return "nil"
	}
	return id.String()
}

// handleBotVoiceStateUpdate handles voice state updates for the bot itself.
func (h *VoiceHandler) handleBotVoiceStateUpdate(e *events.GuildVoiceStateUpdate) {
	// If the bot was disconnected
	if e.VoiceState.ChannelID == nil {
		logger.Info("Bot was disconnected from voice channel",
			zap.String("guild_id", e.VoiceState.GuildID.String()))

		// Clean up the orchestrator state
		if h.orchestrator.IsConnected(e.VoiceState.GuildID.String()) {
			_ = h.orchestrator.LeaveVoice(e.VoiceState.GuildID.String())
		}
	}
}

// maybeLeaveEmptyChannel checks if the voice channel is empty and leaves if so.
func (h *VoiceHandler) maybeLeaveEmptyChannel(e *events.GuildVoiceStateUpdate) {
	guildID := e.VoiceState.GuildID

	// Check if we're connected
	if !h.orchestrator.IsConnected(guildID.String()) {
		return
	}

	// Get the voice connection
	manager := h.orchestrator.GetManager()
	session, exists := manager.GetSession(guildID.String())
	if !exists {
		return
	}

	// Use the event's OldVoiceState to check if someone left our channel
	if e.OldVoiceState.ChannelID != nil && e.OldVoiceState.ChannelID.String() == session.ChannelID {
		// Someone left our channel - check remaining users via member count
		// Simple heuristic: if we can't get accurate count, stay in channel
		logger.Info("User left voice channel, staying for now (empty check not implemented)",
			zap.String("guild_id", guildID.String()),
			zap.String("channel_id", session.ChannelID))
	}
}

// findDefaultTextChannel finds a suitable text channel for fallback messages.
func (h *VoiceHandler) findDefaultTextChannel(guildID snowflake.ID) string {
	channels, err := h.client.Rest.GetGuildChannels(guildID)
	if err != nil {
		return ""
	}

	// Look for the first text channel
	for _, channel := range channels {
		if channel.Type() == discord.ChannelTypeGuildText {
			return channel.ID().String()
		}
	}

	return ""
}

// HandleVoiceServerUpdate handles voice server update events.
// Note: We do NOT forward this to VoiceManager because:
// 1. Disgo's VoiceManager already receives these events from the gateway
// 2. Forwarding causes "voice gateway already connected" errors
// 3. Reconnection logic is handled internally by disgo
func (h *VoiceHandler) HandleVoiceServerUpdate(e *events.VoiceServerUpdate) {
	// No-op - disgo handles VoiceServerUpdate internally
	// Forwarding causes duplicate connection attempts
}
