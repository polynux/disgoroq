package commands

import (
	stdcontext "context"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"polynux/disgoroq/memory"
)

func userMemoryHandler(memoryService memory.Service) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		guildID := getGuildID(e)
		if guildID == "" {
			_ = e.CreateMessage(discord.MessageCreate{Content: "This command can only be used in a server."})
			return
		}

		data := e.SlashCommandInteractionData()
		userOpt, ok := data.Option("user")
		if !ok {
			_ = e.CreateMessage(discord.MessageCreate{Content: "Please specify a user."})
			return
		}

		userID := userOpt.Snowflake().String()

		ctx := stdcontext.Background()
		summary, err := memoryService.GetLatestSummary(ctx, userID, guildID)
		if err != nil {
			_ = e.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("❌ Error retrieving summary: %v", err)})
			return
		}

		if summary == nil {
			_ = e.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("No summary found for <@%s>.", userID)})
			return
		}

		content := fmt.Sprintf("📝 Latest summary for <@%s> (created %s):\n```\n%s\n```",
			userID, summary.CreatedAt.Format("2006-01-02 15:04"), summary.Content)
		_ = e.CreateMessage(discord.MessageCreate{Content: content})
	}
}
