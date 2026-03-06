package handlers

import (
	"context"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
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
// This is used for auto-join functionality.
func (h *VoiceHandler) HandleVoiceStateUpdate(e *events.GuildVoiceStateUpdate) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check if auto-join is enabled for this guild
	autoJoin := h.repo.GetVoiceAutoJoin(ctx, guildID)
	if !autoJoin {
		return
	}

	// Get the configured auto-join channel
	autoJoinChannel, hasChannel := h.repo.GetVoiceAutoJoinChannel(ctx, guildID)
	if !hasChannel || autoJoinChannel == "" {
		return
	}

	// Check if this is a join event to the auto-join channel
	if e.VoiceState.ChannelID != nil && e.VoiceState.ChannelID.String() == autoJoinChannel {
		// Check if we're not already connected
		if !h.orchestrator.IsConnected(guildID) {
			// Check if voice is enabled
			if !h.repo.GetVoiceEnabled(ctx, guildID) {
				return
			}

			logger.Info("Auto-joining voice channel",
				zap.String("guild_id", guildID),
				zap.String("channel_id", autoJoinChannel),
				zap.String("trigger_user_id", userID))

			// Find a text channel for fallback messages
			textChannelID := h.findDefaultTextChannel(e.VoiceState.GuildID)

			// Join the voice channel
			if err := h.orchestrator.JoinVoice(ctx, guildID, autoJoinChannel, textChannelID); err != nil {
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
// This is called when the voice server changes (e.g., during region migration).
func (h *VoiceHandler) HandleVoiceServerUpdate(e *events.VoiceServerUpdate) {
	endpoint := ""
	if e.Endpoint != nil {
		endpoint = *e.Endpoint
	}

	logger.Debug("Voice server update",
		zap.String("guild_id", e.GuildID.String()),
		zap.String("endpoint", endpoint),
		zap.String("token", "***")) // Don't log the token

	// The disgo library handles this automatically via the voice connection
	// We just log it for debugging purposes
}
