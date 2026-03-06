package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/voice"
)

// VoiceCommands contains the dependencies for voice commands.
type VoiceCommands struct {
	repo         *database.Repository
	orchestrator *voice.Orchestrator
	client       *bot.Client
}

// NewVoiceCommands creates a new VoiceCommands instance.
func NewVoiceCommands(repo *database.Repository, orchestrator *voice.Orchestrator, client *bot.Client) *VoiceCommands {
	return &VoiceCommands{
		repo:         repo,
		orchestrator: orchestrator,
		client:       client,
	}
}

// RegisterVoiceCommands registers all voice-related slash commands.
func RegisterVoiceCommands(registry *Registry, voiceCmds *VoiceCommands) {
	if voiceCmds == nil {
		logger.Info("Voice commands not registered - voice orchestrator is nil")
		return
	}

	perms := discord.PermissionManageMessages

	// Main voice command with subcommands
	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "voice",
			Description: "Voice chat commands",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "join",
					Description: "Join your current voice channel",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "leave",
					Description: "Leave the current voice channel",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "status",
					Description: "Show voice chat status",
				},
				discord.ApplicationCommandOptionSubCommandGroup{
					Name:        "autojoin",
					Description: "Configure auto-join settings",
					Options: []discord.ApplicationCommandOptionSubCommand{
						{
							Name:        "enable",
							Description: "Enable auto-join for this voice channel",
							Options: []discord.ApplicationCommandOption{
								discord.ApplicationCommandOptionChannel{
									Name:        "channel",
									Description: "The voice channel to auto-join",
									Required:    true,
									ChannelTypes: []discord.ChannelType{
										discord.ChannelTypeGuildVoice,
									},
								},
							},
						},
						{
							Name:        "disable",
							Description: "Disable auto-join",
						},
						{
							Name:        "status",
							Description: "Show current auto-join settings",
						},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "enable",
					Description: "Enable voice chat for this server",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "disable",
					Description: "Disable voice chat for this server",
				},
			},
			DefaultMemberPermissions: omit.NewPtr(perms),
		},
		voiceCmds.voiceHandler(),
	)
}

// voiceHandler returns the main voice command handler.
func (vc *VoiceCommands) voiceHandler() func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		// Check if orchestrator is available
		if vc.orchestrator == nil {
			vc.respondError(e, "Voice chat is not available")
			return
		}

		data := e.SlashCommandInteractionData()

		// Check for subcommand group first
		if data.SubCommandGroupName != nil && *data.SubCommandGroupName == "autojoin" {
			if data.SubCommandName == nil {
				vc.respondError(e, "No autojoin subcommand specified")
				return
			}
			vc.handleAutojoin(e, *data.SubCommandName)
			return
		}

		// Handle regular subcommands
		subcommand := ""
		if data.SubCommandName != nil {
			subcommand = *data.SubCommandName
		}

		switch subcommand {
		case "join":
			vc.handleJoin(e)
		case "leave":
			vc.handleLeave(e)
		case "status":
			vc.handleStatus(e)
		case "enable":
			vc.handleEnable(e)
		case "disable":
			vc.handleDisable(e)
		default:
			vc.respondError(e, "Unknown subcommand: "+subcommand)
		}
	}
}

// handleJoin handles the /voice join command.
func (vc *VoiceCommands) handleJoin(e *events.ApplicationCommandInteractionCreate) {
	guildID := e.GuildID()
	if guildID == nil {
		vc.respondError(e, "This command can only be used in a guild")
		return
	}
	guildIDStr := guildID.String()

	// Check if already connected
	if vc.orchestrator.IsConnected(guildIDStr) {
		vc.respond(e, "🔊 I'm already in a voice channel! Use `/voice leave` first.")
		return
	}

	// Get member from interaction
	member := e.Member()
	if member == nil {
		vc.respondError(e, "Could not get your user info. Try using `/voice autojoin enable` instead.")
		return
	}

	// Try to get voice state from cache
	voiceState, ok := vc.client.Caches.VoiceState(*guildID, member.User.ID)
	if !ok || voiceState.ChannelID == nil {
		vc.respond(e, "❌ You're not in a voice channel. Join one first, or use `/voice autojoin enable` to set a specific channel.")
		return
	}

	channelID := voiceState.ChannelID.String()

	// Find a text channel for fallback messages
	textChannelID := vc.findTextChannel(*guildID)

	// Use a longer timeout for voice join (DAVE handshake can take 30+ seconds)
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer joinCancel()

	// Join the voice channel
	if err := vc.orchestrator.JoinVoice(joinCtx, guildIDStr, channelID, textChannelID); err != nil {
		logger.Error("Failed to join voice channel",
			zap.String("guild_id", guildIDStr),
			zap.String("channel_id", channelID),
			zap.Error(err))
		vc.respondError(e, "Failed to join voice channel: "+err.Error())
		return
	}

	// Get channel name for response
	channelName := "the voice channel"
	ch, err := vc.client.Rest.GetChannel(*voiceState.ChannelID)
	if err == nil && ch != nil {
		channelName = ch.Name()
	}

	vc.respond(e, fmt.Sprintf("🔊 Joined **%s**! I'll listen and respond when you speak.", channelName))
}

// handleLeave handles the /voice leave command.
func (vc *VoiceCommands) handleLeave(e *events.ApplicationCommandInteractionCreate) {
	guildID := e.GuildID()
	if guildID == nil {
		vc.respondError(e, "This command can only be used in a guild")
		return
	}
	guildIDStr := guildID.String()

	if !vc.orchestrator.IsConnected(guildIDStr) {
		vc.respond(e, "🔇 I'm not in a voice channel.")
		return
	}

	err := vc.orchestrator.LeaveVoice(guildIDStr)
	if err != nil {
		logger.Error("Failed to leave voice channel",
			zap.String("guild_id", guildIDStr),
			zap.Error(err))
		vc.respondError(e, "Failed to leave voice channel: "+err.Error())
		return
	}

	vc.respond(e, "👋 Left the voice channel. See you next time!")
}

// handleStatus handles the /voice status command.
func (vc *VoiceCommands) handleStatus(e *events.ApplicationCommandInteractionCreate) {
	ctx := context.Background()
	guildID := e.GuildID()
	if guildID == nil {
		vc.respondError(e, "This command can only be used in a guild")
		return
	}
	guildIDStr := guildID.String()

	// Get voice settings
	enabled := vc.repo.GetVoiceEnabled(ctx, guildIDStr)
	autoJoin := vc.repo.GetVoiceAutoJoin(ctx, guildIDStr)
	autoJoinChannel, hasAutoJoinChannel := vc.repo.GetVoiceAutoJoinChannel(ctx, guildIDStr)

	// Check current connection
	connected := vc.orchestrator.IsConnected(guildIDStr)
	state := vc.orchestrator.GetState(guildIDStr)

	// Build status message
	status := "**Voice Chat Status**\n\n"
	status += fmt.Sprintf("• Voice Enabled: %s\n", boolEmoji(enabled))
	status += fmt.Sprintf("• Connected: %s\n", boolEmoji(connected))

	if connected {
		status += fmt.Sprintf("• State: %s\n", state.String())

		// Get current voice channel
		manager := vc.orchestrator.GetManager()
		if session, exists := manager.GetSession(guildIDStr); exists {
			channelID, err := snowflake.Parse(session.ChannelID)
			if err == nil {
				channel, err := vc.client.Rest.GetChannel(channelID)
				if err == nil && channel != nil {
					status += fmt.Sprintf("• Channel: %s\n", channel.Name())
				}
			}
		}
	}

	status += fmt.Sprintf("• Auto-Join: %s\n", boolEmoji(autoJoin))
	if hasAutoJoinChannel && autoJoinChannel != "" {
		channelID, err := snowflake.Parse(autoJoinChannel)
		if err == nil {
			channel, err := vc.client.Rest.GetChannel(channelID)
			if err == nil && channel != nil {
				status += fmt.Sprintf("• Auto-Join Channel: %s\n", channel.Name())
			}
		}
	}

	vc.respond(e, status)
}

// handleAutojoin handles autojoin subcommands.
func (vc *VoiceCommands) handleAutojoin(e *events.ApplicationCommandInteractionCreate, subcommand string) {
	ctx := context.Background()
	guildID := e.GuildID()
	if guildID == nil {
		vc.respondError(e, "This command can only be used in a guild")
		return
	}
	guildIDStr := guildID.String()

	switch subcommand {
	case "enable":
		// Get the channel from the command option
		data := e.SlashCommandInteractionData()
		channel := data.Channel("channel")

		ctx := context.Background()
		if err := vc.repo.SetVoiceAutoJoin(ctx, guildIDStr, true); err != nil {
			vc.respondError(e, "Failed to enable auto-join")
			return
		}
		if err := vc.repo.SetVoiceAutoJoinChannel(ctx, guildIDStr, channel.ID.String()); err != nil {
			vc.respondError(e, "Failed to set auto-join channel")
			return
		}

		ch, err := vc.client.Rest.GetChannel(channel.ID)
		channelName := "this channel"
		if err == nil && ch != nil {
			channelName = ch.Name()
		}

		vc.respond(e, fmt.Sprintf("✅ Auto-join enabled for **%s**! I'll automatically join when someone enters.", channelName))

	case "disable":
		if err := vc.repo.SetVoiceAutoJoin(ctx, guildIDStr, false); err != nil {
			vc.respondError(e, "Failed to disable auto-join")
			return
		}
		vc.respond(e, "✅ Auto-join disabled.")

	case "status":
		autoJoin := vc.repo.GetVoiceAutoJoin(ctx, guildIDStr)
		autoJoinChannel, hasChannel := vc.repo.GetVoiceAutoJoinChannel(ctx, guildIDStr)

		status := fmt.Sprintf("**Auto-Join Status**\n• Enabled: %s\n", boolEmoji(autoJoin))
		if hasChannel && autoJoinChannel != "" {
			channelID, err := snowflake.Parse(autoJoinChannel)
			if err == nil {
				channel, err := vc.client.Rest.GetChannel(channelID)
				if err == nil && channel != nil {
					status += fmt.Sprintf("• Channel: %s\n", channel.Name())
				}
			}
		}

		vc.respond(e, status)

	default:
		vc.respondError(e, "Unknown autojoin subcommand: "+subcommand)
	}
}

// handleEnable handles the /voice enable command.
func (vc *VoiceCommands) handleEnable(e *events.ApplicationCommandInteractionCreate) {
	ctx := context.Background()
	guildID := e.GuildID()
	if guildID == nil {
		vc.respondError(e, "This command can only be used in a guild")
		return
	}

	if err := vc.repo.SetVoiceEnabled(ctx, guildID.String(), true); err != nil {
		vc.respondError(e, "Failed to enable voice chat")
		return
	}

	vc.respond(e, "✅ Voice chat enabled for this server!")
}

// handleDisable handles the /voice disable command.
func (vc *VoiceCommands) handleDisable(e *events.ApplicationCommandInteractionCreate) {
	ctx := context.Background()
	guildID := e.GuildID()
	if guildID == nil {
		vc.respondError(e, "This command can only be used in a guild")
		return
	}
	guildIDStr := guildID.String()

	// Leave voice channel if connected
	if vc.orchestrator.IsConnected(guildIDStr) {
		_ = vc.orchestrator.LeaveVoice(guildIDStr)
	}

	if err := vc.repo.SetVoiceEnabled(ctx, guildIDStr, false); err != nil {
		vc.respondError(e, "Failed to disable voice chat")
		return
	}

	vc.respond(e, "✅ Voice chat disabled for this server.")
}

// Helper functions

func (vc *VoiceCommands) respond(e *events.ApplicationCommandInteractionCreate, content string) {
	err := e.CreateMessage(discord.MessageCreate{Content: content})
	if err != nil {
		logger.Error("Failed to respond to interaction", zap.Error(err))
	}
}

func (vc *VoiceCommands) respondError(e *events.ApplicationCommandInteractionCreate, message string) {
	err := e.CreateMessage(discord.MessageCreate{
		Content: "❌ " + message,
		Flags:   discord.MessageFlagEphemeral,
	})
	if err != nil {
		logger.Error("Failed to respond with error", zap.Error(err))
	}
}

func boolEmoji(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

// findTextChannel finds a suitable text channel for fallback messages.
func (vc *VoiceCommands) findTextChannel(guildID snowflake.ID) string {
	channels, err := vc.client.Rest.GetGuildChannels(guildID)
	if err != nil {
		return ""
	}

	for _, channel := range channels {
		if channel.Type() == discord.ChannelTypeGuildText {
			return channel.ID().String()
		}
	}
	return ""
}
