package commands

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/horoscope"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
)

var defaultMemberPermissions int64 = discordgo.PermissionManageMessages

func RegisterAll(registry *Registry, repo *database.Repository, memoryService memory.Service) {
	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with Pong!",
		},
		pingHandler,
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "horoscope",
			Description: "Get the horoscope for a sign",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "sign",
					Description: "The sign for the horoscope (belier, taureau, etc...)",
					Required:    true,
				},
			},
		},
		horoscopeHandler,
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "horoscopechannel",
			Description: "Set the channel for the horoscope",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel for the horoscope",
					Required:    true,
				},
			},
		},
		horoscopeChannelHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "farting_friday_channel",
			Description: "Set the channel for the farting friday",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel for the farting friday",
					Required:    true,
				},
			},
		},
		fartingFridayChannelHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "temperature",
			Description: "Set the temperature for the bot",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "temperature",
					Description: "The temperature for the bot (0.0-1.0)",
					Required:    true,
				},
			},
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		temperatureHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:                     "toggle",
			Description:              "Toggle the bot on or off",
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		toggleHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "threshold",
			Description: "Set the threshold for the bot (activation probability; 0.0-1.0)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "threshold",
					Description: "The threshold activation (0.0-1.0)",
					Required:    true,
				},
			},
		},
		thresholdHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "thresholdsexe",
			Description: "Set the threshold for the bot to say sexe (activation probability; 0.0-1.0)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "thresholdsexe",
					Description: "The thresholdsexe activation (0.0-1.0)",
					Required:    true,
				},
			},
		},
		thresholdSexeHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "messagescount",
			Description: "Set the number of messages to consider for the bot",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "messagescount",
					Description: "The number of messages to consider for the bot (1-100)",
					Required:    true,
				},
			},
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		messagesCountHandler(repo),
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:                     "clean",
			Description:              "Clean the bot's messages",
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		cleanHandler,
	)

	registry.AddCommand(
		&discordgo.ApplicationCommand{
			Name:        "prompt",
			Description: "Set the prompt for the bot",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "set",
					Description: "Set a custom prompt for the bot",
					Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "custom",
							Description: "Set a custom prompt for the bot",
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Options: []*discordgo.ApplicationCommandOption{
								{
									Name:        "prompt",
									Description: "The custom prompt",
									Type:        discordgo.ApplicationCommandOptionString,
									Required:    true,
								},
							},
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
			&discordgo.ApplicationCommand{
				Name:                     "forcesummary",
				Description:              "Force create a summary for a user (admin only)",
				DefaultMemberPermissions: &defaultMemberPermissions,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "The user to summarize",
						Required:    true,
					},
				},
			},
			forceSummaryHandler(memoryService),
		)
	}
}

func pingHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Pong!",
		},
	})
}

func horoscopeHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	sign := i.ApplicationCommandData().Options[0].StringValue()
	horo, err := horoscope.GetHoroscope(sign)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error getting horoscope",
			},
		})
		return
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: horo,
		},
	})
}

func horoscopeChannelHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		channelID := i.ApplicationCommandData().Options[0].ChannelValue(s)
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "horoscope_channel", channelID.ID)
		content := fmt.Sprintf("Horoscope channel set to %v", channelID.ID)
		if err != nil {
			content = "Error setting horoscope channel"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func fartingFridayChannelHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		channelID := i.ApplicationCommandData().Options[0].ChannelValue(s)
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "farting_friday_channel", channelID.ID)
		content := fmt.Sprintf("Farting Friday channel set to %v", channelID.ID)
		if err != nil {
			content = "Error setting farting friday channel"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func temperatureHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		temperature := i.ApplicationCommandData().Options[0].FloatValue()
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "temperature", strconv.FormatFloat(temperature, 'f', -1, 32))
		content := fmt.Sprintf("Temperature set to %v", temperature)
		if err != nil {
			content = "Error setting temperature"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func toggleHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		current := repo.GetState(context.Background(), i.GuildID)
		newState := "off"
		if current == "off" {
			newState = "on"
		}
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "state", newState)
		content := "Bot is now " + newState
		if err != nil {
			content = "Error toggling bot"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func thresholdHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		threshold := i.ApplicationCommandData().Options[0].FloatValue()
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "threshold", strconv.FormatFloat(threshold, 'f', -1, 32))
		content := fmt.Sprintf("Threshold set to %v", threshold)
		if err != nil {
			content = "Error setting threshold"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func thresholdSexeHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		thresholdSexe := i.ApplicationCommandData().Options[0].FloatValue()
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "thresholdSexe", strconv.FormatFloat(thresholdSexe, 'f', -1, 32))
		content := fmt.Sprintf("Threshold set to %v", thresholdSexe)
		if err != nil {
			content = "Error setting sexe threshold"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func messagesCountHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		messagesCount := i.ApplicationCommandData().Options[0].IntValue()
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "messagescount", strconv.FormatInt(messagesCount, 10))
		content := fmt.Sprintf("Messages count set to %v", messagesCount)
		if err != nil {
			content = "Error setting messages count"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func cleanHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	messages, err := s.ChannelMessages(i.ChannelID, 100, "", "", "")
	if err != nil {
		logger.Error("Error getting messages for cleanup", zap.Error(err))
		return
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Cleaning messages...",
		},
	})
	messagesToDelete := make([]string, 0)
	for idx := range messages {
		if messages[idx].Author.ID == s.State.User.ID {
			messagesToDelete = append(messagesToDelete, messages[idx].ID)
		}
	}
	s.ChannelMessagesBulkDelete(i.ChannelID, messagesToDelete)
	str := "Messages cleaned"
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &str,
	})
	time.AfterFunc(10*time.Second, func() {
		s.InteractionResponseDelete(i.Interaction)
	})
}

func promptHandler(repo *database.Repository) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		options := i.ApplicationCommandData().Options
		if options[0].Name != "set" {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Wrong option!",
				},
			})
			return
		}

		options = options[0].Options
		if options[0].Name == "default" {
			content := "Prompt set to default"
			err := repo.DeleteGuildSetting(context.Background(), i.GuildID, "prompt")
			if err != nil {
				content = "Error setting prompt"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
			return
		}

		if options[0].Name != "custom" {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Wrong option!",
				},
			})
			return
		}
		value := options[0].Options[0].StringValue()
		err := repo.SetGuildSetting(context.Background(), i.GuildID, "prompt", value)
		content := "Prompt correctly set"
		if err != nil {
			content = "Error setting prompt"
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
	}
}

func forceSummaryHandler(memoryService memory.Service) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		options := i.ApplicationCommandData().Options
		if len(options) == 0 {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Please specify a user to summarize!",
				},
			})
			return
		}

		user := options[0].UserValue(s)
		if user == nil {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Invalid user!",
				},
			})
			return
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("🔄 Creating summary for <@%s>...", user.ID),
			},
		})

		ctx := context.Background()
		err := memoryService.ForceSummarize(ctx, user.ID, i.GuildID)

		if err != nil {
			errorMsg := fmt.Sprintf("❌ Failed to create summary: %v", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &errorMsg,
			})
			return
		}

		successMsg := fmt.Sprintf("✅ Summary created successfully for <@%s>!", user.ID)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &successMsg,
		})
	}
}
