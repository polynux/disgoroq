package commands

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	appcontext "polynux/disgoroq/context"
	"polynux/disgoroq/database"
)

var ignoreChannelTypes = []discord.ChannelType{
	discord.ChannelTypeGuildText,
	discord.ChannelTypeGuildNews,
	discord.ChannelTypeGuildForum,
	discord.ChannelTypeGuildMedia,
	discord.ChannelTypeGuildPublicThread,
	discord.ChannelTypeGuildPrivateThread,
	discord.ChannelTypeGuildNewsThread,
}

func ignoreCommand() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "ignore",
		Description:              "Manage channels ignored by the bot",
		DefaultMemberPermissions: omit.NewPtr(defaultMemberPermissions),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "add",
				Description: "Ignore a channel (defaults to the current channel)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "The channel to ignore (defaults to current channel)",
						Required:     false,
						ChannelTypes: ignoreChannelTypes,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "remove",
				Description: "Stop ignoring a channel (defaults to the current channel)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "The channel to stop ignoring (defaults to current channel)",
						Required:     false,
						ChannelTypes: ignoreChannelTypes,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "list",
				Description: "List ignored channels in this server",
			},
		},
	}
}

func ignoreHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		guildID := getGuildID(e)
		if guildID == "" {
			replyEphemeral(e, "This command can only be used in a server.")
			return
		}

		data := e.SlashCommandInteractionData()
		subcommandName := ""
		if data.SubCommandName != nil {
			subcommandName = *data.SubCommandName
		}

		switch subcommandName {
		case "add":
			handleIgnoreAdd(e, repo, guildID)
		case "remove":
			handleIgnoreRemove(e, repo, guildID)
		case "list":
			handleIgnoreList(e, repo, guildID)
		default:
			replyEphemeral(e, "Unknown subcommand!")
		}
	}
}

func resolveIgnoreTargetChannel(e *events.ApplicationCommandInteractionCreate) snowflake.ID {
	if ch, ok := e.SlashCommandInteractionData().OptChannel("channel"); ok {
		return ch.ID
	}
	return e.Channel().ID()
}

func handleIgnoreAdd(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	channelID := resolveIgnoreTargetChannel(e)
	if channelID == 0 {
		replyEphemeral(e, "Could not determine the channel to ignore.")
		return
	}

	ctx, cancel := appcontext.Command()
	defer cancel()

	changed, err := repo.AddIgnoredChannel(ctx, guildID, channelID.String())
	if err != nil {
		replyEphemeral(e, "Error adding ignored channel: "+err.Error())
		return
	}
	if !changed {
		replyEphemeral(e, fmt.Sprintf("<#%s> is already ignored.", channelID))
		return
	}
	replyEphemeral(e, fmt.Sprintf("✅ <#%s> is now ignored.", channelID))
}

func handleIgnoreRemove(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	channelID := resolveIgnoreTargetChannel(e)
	if channelID == 0 {
		replyEphemeral(e, "Could not determine the channel to unignore.")
		return
	}

	ctx, cancel := appcontext.Command()
	defer cancel()

	changed, err := repo.RemoveIgnoredChannel(ctx, guildID, channelID.String())
	if err != nil {
		replyEphemeral(e, "Error removing ignored channel: "+err.Error())
		return
	}
	if !changed {
		replyEphemeral(e, fmt.Sprintf("<#%s> is not ignored.", channelID))
		return
	}
	replyEphemeral(e, fmt.Sprintf("✅ <#%s> is no longer ignored.", channelID))
}

func handleIgnoreList(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	ctx, cancel := appcontext.Command()
	defer cancel()

	channels, err := repo.GetIgnoredChannels(ctx, guildID)
	if err != nil {
		replyEphemeral(e, "Error reading ignored channels: "+err.Error())
		return
	}
	if len(channels) == 0 {
		replyEphemeral(e, "No channels are ignored in this server.")
		return
	}

	mentions := make([]string, len(channels))
	for i, id := range channels {
		mentions[i] = fmt.Sprintf("<#%s>", id)
	}
	replyEphemeral(e, "Ignored channels: "+strings.Join(mentions, ", "))
}

func replyEphemeral(e *events.ApplicationCommandInteractionCreate, content string) {
	_ = e.CreateMessage(discord.MessageCreate{
		Content:         content,
		Flags:           discord.MessageFlagEphemeral,
		AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}},
	})
}
