package commands

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/voice"
)

// VoiceCommands contains the dependencies for voice commands.
type VoiceCommands struct {
	repo         *database.Repository
	orchestrator *voice.Orchestrator
}

// NewVoiceCommands creates a new VoiceCommands instance.
func NewVoiceCommands(repo *database.Repository, orchestrator *voice.Orchestrator) *VoiceCommands {
	return &VoiceCommands{
		repo:         repo,
		orchestrator: orchestrator,
	}
}

// RegisterVoiceCommands registers all voice-related slash commands.
func RegisterVoiceCommands(registry *Registry, voiceCmds *VoiceCommands) {
	if voiceCmds == nil {
		logger.Info("Voice commands not registered - voice orchestrator is nil")
		return
	}

	// Main voice command with subcommands
	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "voice",
			Description: "Voice chat commands",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "join",
					Description: "Join your current voice channel",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "leave",
					Description: "Leave the current voice channel",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "status",
					Description: "Show voice chat status",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "autojoin",
					Description: "Configure auto-join settings",
					Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "enable",
							Description: "Enable auto-join for this voice channel",
							Type:        discordgo.ApplicationCommandOptionSubCommand,
						},
						{
							Name:        "disable",
							Description: "Disable auto-join",
							Type:        discordgo.ApplicationCommandOptionSubCommand,
						},
						{
							Name:        "status",
							Description: "Show current auto-join settings",
							Type:        discordgo.ApplicationCommandOptionSubCommand,
						},
					},
				},
				{
					Name:        "enable",
					Description: "Enable voice chat for this server",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "disable",
					Description: "Disable voice chat for this server",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		voiceCmds.voiceHandler(),
	)
}

// voiceHandler returns the main voice command handler.
func (vc *VoiceCommands) voiceHandler() func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// Check if orchestrator is available
		if vc.orchestrator == nil {
			respondError(s, i, "Voice chat is not available")
			return
		}

		options := i.ApplicationCommandData().Options
		if len(options) == 0 {
			respondError(s, i, "No subcommand specified")
			return
		}

		subcommand := options[0].Name

		// Handle subcommand groups
		if subcommand == "autojoin" {
			if len(options[0].Options) == 0 {
				respondError(s, i, "No autojoin subcommand specified")
				return
			}
			vc.handleAutojoin(s, i, options[0].Options[0].Name)
			return
		}

		switch subcommand {
		case "join":
			vc.handleJoin(s, i)
		case "leave":
			vc.handleLeave(s, i)
		case "status":
			vc.handleStatus(s, i)
		case "enable":
			vc.handleEnable(s, i)
		case "disable":
			vc.handleDisable(s, i)
		default:
			respondError(s, i, "Unknown subcommand: "+subcommand)
		}
	}
}

// handleJoin handles the /voice join command.
func (vc *VoiceCommands) handleJoin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	guildID := i.GuildID

	// Check if already connected
	if vc.orchestrator.IsConnected(guildID) {
		respond(s, i, "🔊 I'm already in a voice channel! Use `/voice leave` first.")
		return
	}

	// Get the user's voice state
	vs, err := s.State.VoiceState(guildID, i.Member.User.ID)
	if err != nil || vs == nil || vs.ChannelID == "" {
		respond(s, i, "❌ You need to be in a voice channel first!")
		return
	}

	// Join the voice channel
	err = vc.orchestrator.JoinVoice(ctx, guildID, vs.ChannelID, i.ChannelID)
	if err != nil {
		logger.Error("Failed to join voice channel",
			zap.String("guild_id", guildID),
			zap.String("channel_id", vs.ChannelID),
			zap.Error(err))
		respondError(s, i, "Failed to join voice channel: "+err.Error())
		return
	}

	// Get channel name for display
	channel, _ := s.Channel(vs.ChannelID)
	channelName := "voice channel"
	if channel != nil {
		channelName = channel.Name
	}

	respond(s, i, fmt.Sprintf("🔊 Joined **%s**! I'm ready to listen. Say something!", channelName))
}

// handleLeave handles the /voice leave command.
func (vc *VoiceCommands) handleLeave(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	if !vc.orchestrator.IsConnected(guildID) {
		respond(s, i, "🔇 I'm not in a voice channel.")
		return
	}

	err := vc.orchestrator.LeaveVoice(guildID)
	if err != nil {
		logger.Error("Failed to leave voice channel",
			zap.String("guild_id", guildID),
			zap.Error(err))
		respondError(s, i, "Failed to leave voice channel: "+err.Error())
		return
	}

	respond(s, i, "👋 Left the voice channel. See you next time!")
}

// handleStatus handles the /voice status command.
func (vc *VoiceCommands) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	guildID := i.GuildID

	// Get voice settings
	enabled := vc.repo.GetVoiceEnabled(ctx, guildID)
	autoJoin := vc.repo.GetVoiceAutoJoin(ctx, guildID)
	autoJoinChannel, hasAutoJoinChannel := vc.repo.GetVoiceAutoJoinChannel(ctx, guildID)

	// Check current connection
	connected := vc.orchestrator.IsConnected(guildID)
	state := vc.orchestrator.GetState(guildID)

	// Build status message
	status := "**Voice Chat Status**\n\n"
	status += fmt.Sprintf("• Voice Enabled: %s\n", boolEmoji(enabled))
	status += fmt.Sprintf("• Connected: %s\n", boolEmoji(connected))

	if connected {
		status += fmt.Sprintf("• State: %s\n", state.String())

		// Get current voice channel
		manager := vc.orchestrator.GetManager()
		if session, exists := manager.GetSession(guildID); exists {
			channel, _ := s.Channel(session.ChannelID)
			if channel != nil {
				status += fmt.Sprintf("• Channel: %s\n", channel.Name)
			}
		}
	}

	status += fmt.Sprintf("• Auto-Join: %s\n", boolEmoji(autoJoin))
	if hasAutoJoinChannel && autoJoinChannel != "" {
		channel, _ := s.Channel(autoJoinChannel)
		if channel != nil {
			status += fmt.Sprintf("• Auto-Join Channel: %s\n", channel.Name)
		}
	}

	respond(s, i, status)
}

// handleAutojoin handles autojoin subcommands.
func (vc *VoiceCommands) handleAutojoin(s *discordgo.Session, i *discordgo.InteractionCreate, subcommand string) {
	ctx := context.Background()
	guildID := i.GuildID

	switch subcommand {
	case "enable":
		// Get user's current voice channel
		vs, err := s.State.VoiceState(guildID, i.Member.User.ID)
		if err != nil || vs == nil || vs.ChannelID == "" {
			respond(s, i, "❌ You need to be in a voice channel to set auto-join!")
			return
		}

		// Save settings
		if err := vc.repo.SetVoiceAutoJoin(ctx, guildID, true); err != nil {
			respondError(s, i, "Failed to enable auto-join")
			return
		}
		if err := vc.repo.SetVoiceAutoJoinChannel(ctx, guildID, vs.ChannelID); err != nil {
			respondError(s, i, "Failed to set auto-join channel")
			return
		}

		channel, _ := s.Channel(vs.ChannelID)
		channelName := "this channel"
		if channel != nil {
			channelName = channel.Name
		}

		respond(s, i, fmt.Sprintf("✅ Auto-join enabled for **%s**! I'll automatically join when someone enters.", channelName))

	case "disable":
		if err := vc.repo.SetVoiceAutoJoin(ctx, guildID, false); err != nil {
			respondError(s, i, "Failed to disable auto-join")
			return
		}
		respond(s, i, "✅ Auto-join disabled.")

	case "status":
		autoJoin := vc.repo.GetVoiceAutoJoin(ctx, guildID)
		autoJoinChannel, hasChannel := vc.repo.GetVoiceAutoJoinChannel(ctx, guildID)

		status := fmt.Sprintf("**Auto-Join Status**\n• Enabled: %s\n", boolEmoji(autoJoin))
		if hasChannel && autoJoinChannel != "" {
			channel, _ := s.Channel(autoJoinChannel)
			if channel != nil {
				status += fmt.Sprintf("• Channel: %s\n", channel.Name)
			}
		}

		respond(s, i, status)

	default:
		respondError(s, i, "Unknown autojoin subcommand: "+subcommand)
	}
}

// handleEnable handles the /voice enable command.
func (vc *VoiceCommands) handleEnable(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()

	if err := vc.repo.SetVoiceEnabled(ctx, i.GuildID, true); err != nil {
		respondError(s, i, "Failed to enable voice chat")
		return
	}

	respond(s, i, "✅ Voice chat enabled for this server!")
}

// handleDisable handles the /voice disable command.
func (vc *VoiceCommands) handleDisable(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()

	// Leave voice channel if connected
	if vc.orchestrator.IsConnected(i.GuildID) {
		_ = vc.orchestrator.LeaveVoice(i.GuildID)
	}

	if err := vc.repo.SetVoiceEnabled(ctx, i.GuildID, false); err != nil {
		respondError(s, i, "Failed to disable voice chat")
		return
	}

	respond(s, i, "✅ Voice chat disabled for this server.")
}

// Helper functions

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
	if err != nil {
		logger.Error("Failed to respond to interaction", zap.Error(err))
	}
}

func respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "❌ " + message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
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