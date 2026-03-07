package commands

import (
	stdcontext "context"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	appcontext "polynux/disgoroq/context"
	"polynux/disgoroq/database"
	"polynux/disgoroq/horoscope"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
	"polynux/disgoroq/voice"
)

var defaultMemberPermissions = discord.PermissionManageMessages

var defaultPrompt string

// RegisterAll registers all bot commands.
// If voiceOrchestrator is nil, voice commands will not be registered.
func RegisterAll(registry *Registry, repo *database.Repository, memoryService memory.Service, cfgDefaultPrompt string, voiceOrchestrator *voice.Orchestrator, client *bot.Client) {
	defaultPrompt = cfgDefaultPrompt

	// Register voice commands if orchestrator is available
	if voiceOrchestrator != nil {
		voiceCmds := NewVoiceCommands(repo, voiceOrchestrator, client)
		RegisterVoiceCommands(registry, voiceCmds)
	}

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "ping",
			Description: "Replies with Pong!",
		},
		pingHandler,
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "horoscope",
			Description: "Get the horoscope for a sign",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{
					Name:        "sign",
					Description: "The sign for the horoscope (belier, taureau, etc...)",
					Required:    true,
				},
			},
		},
		horoscopeHandler,
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "horoscopechannel",
			Description: "Set the channel for the horoscope",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionChannel{
					Name:        "channel",
					Description: "The channel for the horoscope",
					Required:    true,
				},
			},
		},
		horoscopeChannelHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "farting_friday_channel",
			Description: "Set the channel for the farting friday",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionChannel{
					Name:        "channel",
					Description: "The channel for the farting friday",
					Required:    true,
				},
			},
		},
		fartingFridayChannelHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "temperature",
			Description: "Set the temperature for the bot",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionFloat{
					Name:        "temperature",
					Description: "The temperature for the bot (0.0-1.0)",
					Required:    true,
				},
			},
			DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
		},
		temperatureHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:                     "toggle",
			Description:              "Toggle the bot on or off",
			DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
		},
		toggleHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "threshold",
			Description: "Set the threshold for the bot (activation probability; 0.0-1.0)",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionFloat{
					Name:        "threshold",
					Description: "The threshold activation (0.0-1.0)",
					Required:    true,
				},
			},
		},
		thresholdHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "thresholdsexe",
			Description: "Set the threshold for the bot to say sexe (activation probability; 0.0-1.0)",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionFloat{
					Name:        "thresholdsexe",
					Description: "The thresholdsexe activation (0.0-1.0)",
					Required:    true,
				},
			},
		},
		thresholdSexeHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "messagescount",
			Description: "Set the number of messages to consider for the bot",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionInt{
					Name:        "messagescount",
					Description: "The number of messages to consider for the bot (1-100)",
					Required:    true,
				},
			},
			DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
		},
		messagesCountHandler(repo),
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:                     "clean",
			Description:              "Clean the bot's messages",
			DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
		},
		cleanHandler,
	)

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:        "prompt",
			Description: "Manage the bot's system prompt",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "see",
					Description: "View the current prompt",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "append",
					Description: "Append text to the current prompt",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionString{
							Name:        "text",
							Description: "Text to append to the prompt",
							Required:    true,
							MaxLength:   ptr(1000),
						},
					},
				},
				discord.ApplicationCommandOptionSubCommandGroup{
					Name:        "set",
					Description: "Set a custom prompt for the bot",
					Options: []discord.ApplicationCommandOptionSubCommand{
						{
							Name:        "custom",
							Description: "Set a custom prompt for the bot",
							Options: []discord.ApplicationCommandOption{
								discord.ApplicationCommandOptionString{
									Name:        "prompt",
									Description: "The custom prompt for the bot",
									Required:    true,
									MaxLength:   ptr(1000),
								},
							},
						},
						{
							Name:        "default",
							Description: "Put back the default prompt",
						},
					},
				},
			},
		},
		promptHandler(repo),
	)

	// Memory management commands
	if memoryService != nil {
		registry.AddCommand(
			discord.SlashCommandCreate{
				Name:                     "forcesummary",
				Description:              "Force create a summary for a user (admin only)",
				DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        "user",
						Description: "The user to summarize",
						Required:    true,
					},
				},
			},
			forceSummaryHandler(memoryService),
		)
	}

	registry.AddCommand(
		discord.SlashCommandCreate{
			Name:                     "reengage",
			Description:              "Configure channel reengagement settings",
			DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "toggle",
					Description: "Toggle reengagement for this channel",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "chance",
					Description: "Set reengage chance (0.0-1.0)",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionFloat{
							Name:        "value",
							Description: "The chance value (0.01 = 1%)",
							Required:    true,
						},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "threshold",
					Description: "Set inactivity threshold in minutes",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionInt{
							Name:        "minutes",
							Description: "Minutes of inactivity before reengage can trigger",
							Required:    true,
						},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "message",
					Description: "Set the reengage message prompt",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionString{
							Name:        "text",
							Description: "The message appended to the prompt when reengaging",
							Required:    true,
							MaxLength:   ptr(500),
						},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "status",
					Description: "Show current reengage settings for this channel",
				},
			},
		},
		reengageHandler(repo),
	)
}

func ptr[T any](v T) *T {
	return &v
}

func getGuildID(e *events.ApplicationCommandInteractionCreate) string {
	guildID := e.GuildID()
	if guildID == nil {
		return ""
	}
	return guildID.String()
}

func pingHandler(e *events.ApplicationCommandInteractionCreate) {
	_ = e.CreateMessage(discord.MessageCreate{Content: "Pong!"})
}

func horoscopeHandler(e *events.ApplicationCommandInteractionCreate) {
	data := e.SlashCommandInteractionData()
	sign := data.String("sign")
	horo, err := horoscope.GetHoroscope(sign)
	if err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error getting horoscope"})
		return
	}
	_ = e.CreateMessage(discord.MessageCreate{Content: horo})
}

func horoscopeChannelHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		channel := data.Channel("channel")
		ctx := stdcontext.Background()
		err := repo.SetGuildSetting(ctx, getGuildID(e), "horoscope_channel", channel.ID.String())
		content := fmt.Sprintf("Horoscope channel set to <#%s>", channel.ID)
		if err != nil {
			content = "Error setting horoscope channel"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func fartingFridayChannelHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		channel := data.Channel("channel")
		ctx := stdcontext.Background()
		err := repo.SetGuildSetting(ctx, getGuildID(e), "farting_friday_channel", channel.ID.String())
		content := fmt.Sprintf("Farting Friday channel set to <#%s>", channel.ID)
		if err != nil {
			content = "Error setting farting friday channel"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func temperatureHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		temperature := data.Float("temperature")
		ctx := stdcontext.Background()
		err := repo.SetGuildSetting(ctx, getGuildID(e), "temperature", strconv.FormatFloat(temperature, 'f', -1, 32))
		content := fmt.Sprintf("Temperature set to %v", temperature)
		if err != nil {
			content = "Error setting temperature"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func toggleHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		ctx := stdcontext.Background()
		current := repo.GetState(ctx, getGuildID(e))
		newState := "off"
		if current == "off" {
			newState = "on"
		}
		err := repo.SetGuildSetting(ctx, getGuildID(e), "state", newState)
		content := "Bot is now " + newState
		if err != nil {
			content = "Error toggling bot"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func thresholdHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		threshold := data.Float("threshold")
		ctx := stdcontext.Background()
		err := repo.SetGuildSetting(ctx, getGuildID(e), "threshold", strconv.FormatFloat(threshold, 'f', -1, 32))
		content := fmt.Sprintf("Threshold set to %v", threshold)
		if err != nil {
			content = "Error setting threshold"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func thresholdSexeHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		thresholdSexe := data.Float("thresholdsexe")
		ctx := stdcontext.Background()
		err := repo.SetGuildSetting(ctx, getGuildID(e), "thresholdSexe", strconv.FormatFloat(thresholdSexe, 'f', -1, 32))
		content := fmt.Sprintf("Threshold set to %v", thresholdSexe)
		if err != nil {
			content = "Error setting sexe threshold"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func messagesCountHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		messagesCount := data.Int("messagescount")
		ctx := stdcontext.Background()
		err := repo.SetGuildSetting(ctx, getGuildID(e), "messagescount", strconv.Itoa(messagesCount))
		content := fmt.Sprintf("Messages count set to %v", messagesCount)
		if err != nil {
			content = "Error setting messages count"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func cleanHandler(e *events.ApplicationCommandInteractionCreate) {
	ctx, cancel := appcontext.Message()
	defer cancel()

	channel := e.Channel()
	channelID := channel.ID()
	_ = e.DeferCreateMessage(false)

	messages, err := e.Client().Rest.GetMessages(channelID, 0, 0, 0, 100, rest.WithCtx(ctx))
	if err != nil {
		logger.Error("Error getting messages for cleanup", zap.Error(err))
		_, _ = e.Client().Rest.CreateFollowupMessage(e.Client().ID(), e.Token(), discord.MessageCreate{Content: "Error getting messages"})
		return
	}

	messagesToDelete := make([]snowflake.ID, 0)
	botID := e.Client().ID()
	for _, msg := range messages {
		if msg.Author.ID == botID {
			messagesToDelete = append(messagesToDelete, msg.ID)
		}
	}

	if len(messagesToDelete) > 0 {
		_ = e.Client().Rest.BulkDeleteMessages(channelID, messagesToDelete, rest.WithCtx(ctx))
	}

	content := "Messages cleaned"
	_, _ = e.Client().Rest.CreateFollowupMessage(e.Client().ID(), e.Token(), discord.MessageCreate{Content: content})
}

func promptHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		subcommandName := ""
		if data.SubCommandName != nil {
			subcommandName = *data.SubCommandName
		}
		subGroupName := ""
		if data.SubCommandGroupName != nil {
			subGroupName = *data.SubCommandGroupName
		}

		// Handle subcommand groups (e.g., "set" -> "custom" or "default")
		if subGroupName == "set" {
			handlePromptSet(e, repo)
			return
		}

		switch subcommandName {
		case "see":
			handlePromptSee(e, repo)
		case "append":
			handlePromptAppend(e, repo)
		default:
			_ = e.CreateMessage(discord.MessageCreate{Content: "Unknown subcommand!"})
		}
	}
}

func handlePromptSee(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	prompt, hasCustom := repo.GetPrompt(ctx, getGuildID(e))

	if !hasCustom {
		guildID := e.GuildID()
		if guildID == nil {
			_ = e.CreateMessage(discord.MessageCreate{Content: "Error getting bot information"})
			return
		}
		botMember, err := e.Client().Rest.GetMember(*guildID, e.Client().ID(), rest.WithCtx(ctx))
		if err != nil {
			_ = e.CreateMessage(discord.MessageCreate{Content: "Error getting bot information"})
			return
		}
		botNick := ""
		if botMember.Nick != nil {
			botNick = *botMember.Nick
		}
		if botNick == "" {
			botNick = botMember.User.Username
		}
		prompt = getDefaultPrompt(botNick)
	}

	// Truncate if too long for Discord message (max 2000, leave room for prefix)
	content := prompt
	if len(content) > 1900 {
		content = content[:1900] + "\n... (truncated)"
	}

	prefix := "**Current prompt:**\n"
	if !hasCustom {
		prefix = "**Current prompt (default):**\n"
	}

	_ = e.CreateMessage(discord.MessageCreate{Content: prefix + content})
}

func handlePromptAppend(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	data := e.SlashCommandInteractionData()
	textToAppend := data.String("text")

	currentPrompt, hasCustom := repo.GetPrompt(ctx, getGuildID(e))
	if !hasCustom {
		guildID := e.GuildID()
		if guildID == nil {
			_ = e.CreateMessage(discord.MessageCreate{Content: "Error getting bot information"})
			return
		}
		botMember, err := e.Client().Rest.GetMember(*guildID, e.Client().ID(), rest.WithCtx(ctx))
		if err != nil {
			_ = e.CreateMessage(discord.MessageCreate{Content: "Error getting bot information"})
			return
		}
		botNick := ""
		if botMember.Nick != nil {
			botNick = *botMember.Nick
		}
		if botNick == "" {
			botNick = botMember.User.Username
		}
		currentPrompt = getDefaultPrompt(botNick)
	}

	newPrompt := currentPrompt + "\n" + textToAppend

	err := repo.SetGuildSetting(ctx, getGuildID(e), "prompt", newPrompt)
	content := "Prompt updated successfully"
	if err != nil {
		content = "Error updating prompt"
	}

	_ = e.CreateMessage(discord.MessageCreate{Content: content})
}

func handlePromptSet(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	data := e.SlashCommandInteractionData()

	subName := ""
	if data.SubCommandName != nil {
		subName = *data.SubCommandName
	}

	if subName == "default" {
		content := "Prompt set to default"
		err := repo.DeleteGuildSetting(ctx, getGuildID(e), "prompt")
		if err != nil {
			content = "Error setting prompt"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
		return
	}

	// Must be "custom"
	value := data.String("prompt")
	err := repo.SetGuildSetting(ctx, getGuildID(e), "prompt", value)
	content := "Prompt correctly set"
	if err != nil {
		content = "Error setting prompt"
	}
	_ = e.CreateMessage(discord.MessageCreate{Content: content})
}

func forceSummaryHandler(memoryService memory.Service) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		userOpt, ok := data.Option("user")
		if !ok {
			_ = e.CreateMessage(discord.MessageCreate{Content: "Please specify a user to summarize!"})
			return
		}

		userID := userOpt.Snowflake()

		_ = e.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("🔄 Creating summary for <@%s>...", userID)})

		ctx := stdcontext.Background()
		err := memoryService.ForceSummarize(ctx, userID.String(), getGuildID(e))

		if err != nil {
			errorMsg := fmt.Sprintf("❌ Failed to create summary: %v", err)
			_, _ = e.Client().Rest.CreateFollowupMessage(e.Client().ID(), e.Token(), discord.MessageCreate{Content: errorMsg})
			return
		}

		successMsg := fmt.Sprintf("✅ Summary created successfully for <@%s>!", userID)
		_, _ = e.Client().Rest.CreateFollowupMessage(e.Client().ID(), e.Token(), discord.MessageCreate{Content: successMsg})
	}
}

func reengageHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		data := e.SlashCommandInteractionData()
		subcommandName := ""
		if data.SubCommandName != nil {
			subcommandName = *data.SubCommandName
		}

		switch subcommandName {
		case "toggle":
			handleReengageToggle(e, repo)
		case "chance":
			handleReengageChance(e, repo)
		case "threshold":
			handleReengageThreshold(e, repo)
		case "message":
			handleReengageMessage(e, repo)
		case "status":
			handleReengageStatus(e, repo)
		default:
			_ = e.CreateMessage(discord.MessageCreate{Content: "Unknown subcommand!"})
		}
	}
}

func handleReengageToggle(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	channel := e.Channel()
	channelID := channel.ID()
	enabled := repo.GetReengageEnabled(ctx, getGuildID(e), channelID.String())

	if enabled {
		err := repo.DeleteReengageConfig(ctx, getGuildID(e), channelID.String())
		content := "✅ Reengagement disabled for this channel!"
		if err != nil {
			content = "❌ Error disabling reengagement"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	} else {
		err := repo.SetReengageEnabled(ctx, getGuildID(e), channelID.String(), true)
		content := "✅ Reengagement enabled for this channel!"
		if err != nil {
			content = "❌ Error enabling reengagement"
		}
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}

func handleReengageChance(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	data := e.SlashCommandInteractionData()
	chance := data.Float("value")
	channel := e.Channel()
	channelID := channel.ID()
	err := repo.SetReengageChance(ctx, getGuildID(e), channelID.String(), chance)
	content := fmt.Sprintf("✅ Reengage chance set to %.2f%% (%.4f)", chance*100, chance)
	if err != nil {
		content = "❌ Error setting reengage chance"
	}
	_ = e.CreateMessage(discord.MessageCreate{Content: content})
}

func handleReengageThreshold(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	data := e.SlashCommandInteractionData()
	minutes := data.Int("minutes")
	channel := e.Channel()
	channelID := channel.ID()
	err := repo.SetReengageThreshold(ctx, getGuildID(e), channelID.String(), int(minutes))
	content := fmt.Sprintf("✅ Reengage threshold set to %d minutes", minutes)
	if err != nil {
		content = "❌ Error setting reengage threshold"
	}
	_ = e.CreateMessage(discord.MessageCreate{Content: content})
}

func handleReengageMessage(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	data := e.SlashCommandInteractionData()
	message := data.String("text")
	err := repo.SetReengageMessage(ctx, getGuildID(e), message)
	content := fmt.Sprintf("✅ Reengage message set!\n```\n%s\n```", message)
	if err != nil {
		content = "❌ Error setting reengage message"
	}
	_ = e.CreateMessage(discord.MessageCreate{Content: content})
}

func handleReengageStatus(e *events.ApplicationCommandInteractionCreate, repo *database.Repository) {
	ctx := stdcontext.Background()
	channel := e.Channel()
	channelID := channel.ID()
	enabled, chance, threshold := repo.GetReengageConfig(ctx, getGuildID(e), channelID.String())

	status := "❌ Disabled"
	if enabled {
		status = "✅ Enabled"
	}

	message, hasMessage := repo.GetReengageMessage(ctx, getGuildID(e))
	messageDisplay := "(using default)"
	if hasMessage {
		if len(message) > 100 {
			messageDisplay = message[:100] + "..."
		} else {
			messageDisplay = message
		}
	}

	content := fmt.Sprintf("**Reengage Settings for this channel:**\nStatus: %s\nChance: %.2f%%\nThreshold: %d minutes\nMessage: %s", status, chance*100, threshold, messageDisplay)
	_ = e.CreateMessage(discord.MessageCreate{Content: content})
}

func getDefaultPrompt(botNick string) string {
	tmpl, err := template.New("prompt").Parse(defaultPrompt)
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
