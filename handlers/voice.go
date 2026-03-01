package handlers

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/voice"
)

// VoiceHandler handles Discord voice state events.
type VoiceHandler struct {
	orchestrator *voice.Orchestrator
	repo         *database.Repository
}

// NewVoiceHandler creates a new voice handler.
func NewVoiceHandler(orchestrator *voice.Orchestrator, repo *database.Repository) *VoiceHandler {
	return &VoiceHandler{
		orchestrator: orchestrator,
		repo:         repo,
	}
}

// HandleVoiceStateUpdate handles voice state update events from Discord.
// This is used for auto-join functionality.
func (h *VoiceHandler) HandleVoiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	// Skip if orchestrator or repo is not initialized
	if h.orchestrator == nil || h.repo == nil {
		return
	}

	guildID := vsu.GuildID
	userID := vsu.UserID

	// Skip events for the bot itself
	if userID == s.State.User.ID {
		h.handleBotVoiceStateUpdate(s, vsu)
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
	if vsu.ChannelID == autoJoinChannel {
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
			textChannelID := h.findDefaultTextChannel(s, guildID)

			// Join the voice channel
			if err := h.orchestrator.JoinVoice(ctx, guildID, autoJoinChannel, textChannelID); err != nil {
				logger.Error("Failed to auto-join voice channel",
					zap.String("guild_id", guildID),
					zap.String("channel_id", autoJoinChannel),
					zap.Error(err))
			}
		}
	} else if vsu.ChannelID == "" {
		// User left a voice channel - check if we should leave too
		h.maybeLeaveEmptyChannel(s, guildID)
	}
}

// handleBotVoiceStateUpdate handles voice state updates for the bot itself.
func (h *VoiceHandler) handleBotVoiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	// If the bot was disconnected
	if vsu.ChannelID == "" {
		logger.Info("Bot was disconnected from voice channel",
			zap.String("guild_id", vsu.GuildID))

		// Clean up the orchestrator state
		if h.orchestrator.IsConnected(vsu.GuildID) {
			_ = h.orchestrator.LeaveVoice(vsu.GuildID)
		}
	}
}

// maybeLeaveEmptyChannel checks if the voice channel is empty and leaves if so.
func (h *VoiceHandler) maybeLeaveEmptyChannel(s *discordgo.Session, guildID string) {
	// Check if we're connected
	if !h.orchestrator.IsConnected(guildID) {
		return
	}

	// Get the voice connection
	manager := h.orchestrator.GetManager()
	session, exists := manager.GetSession(guildID)
	if !exists {
		return
	}

	// Count users in the channel
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return
	}

	usersInChannel := 0
	for _, vs := range guild.VoiceStates {
		if vs.ChannelID == session.ChannelID && vs.UserID != s.State.User.ID {
			usersInChannel++
		}
	}

	// If no users left, leave the channel
	if usersInChannel == 0 {
		logger.Info("Voice channel is empty, leaving",
			zap.String("guild_id", guildID),
			zap.String("channel_id", session.ChannelID))

		_ = h.orchestrator.LeaveVoice(guildID)
	}
}

// findDefaultTextChannel finds a suitable text channel for fallback messages.
func (h *VoiceHandler) findDefaultTextChannel(s *discordgo.Session, guildID string) string {
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return ""
	}

	// Look for the first text channel where the bot can send messages
	for _, channel := range guild.Channels {
		if channel.Type == discordgo.ChannelTypeGuildText {
			// Check if the bot has permission to send messages
			perms, err := s.State.UserChannelPermissions(s.State.User.ID, channel.ID)
			if err != nil {
				continue
			}
			if perms&discordgo.PermissionSendMessages != 0 {
				return channel.ID
			}
		}
	}

	return ""
}

// HandleVoiceServerUpdate handles voice server update events.
// This is called when the voice server changes (e.g., during region migration).
func (h *VoiceHandler) HandleVoiceServerUpdate(s *discordgo.Session, vsu *discordgo.VoiceServerUpdate) {
	logger.Debug("Voice server update",
		zap.String("guild_id", vsu.GuildID),
		zap.String("endpoint", vsu.Endpoint),
		zap.String("token", "***")) // Don't log the token

	// The discordgo library handles this automatically via the voice connection
	// We just log it for debugging purposes
}