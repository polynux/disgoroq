package commands

import (
	stdcontext "context"
	"fmt"
	"time"
	"unicode"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

const (
	promptPageDescriptionLimit = 3800
	promptPaginatorTimeout     = 2 * time.Minute
	promptPaginatorColor       = 0x5865F2
	promptPrevEmoji            = "⬅️"
	promptNextEmoji            = "➡️"
)

func sendPaginatedPrompt(e *events.ApplicationCommandInteractionCreate, title string, prompt string) {
	appPermissions := e.AppPermissions()
	if appPermissions == nil || appPermissions.Missing(discord.PermissionManageMessages) {
		_ = e.CreateMessage(discord.MessageCreate{
			Content: "The bot needs the Manage Messages permission in this channel to paginate prompts.",
		})
		return
	}

	pages := splitPromptPages(prompt, promptPageDescriptionLimit)
	embeds := buildPromptEmbeds(title, prompt, pages)

	if err := e.CreateMessage(discord.MessageCreate{
		Embeds: []discord.Embed{embeds[0]},
	}); err != nil {
		logger.Error("Failed to create prompt paginator message", zap.Error(err))
		return
	}

	if len(embeds) == 1 {
		return
	}

	ctx := stdcontext.Background()
	message, err := e.Client().Rest.GetInteractionResponse(e.ApplicationID(), e.Token(), rest.WithCtx(ctx))
	if err != nil {
		logger.Error("Failed to get prompt paginator message",
			zap.Error(err),
			zap.String("user_id", e.User().ID.String()))
		return
	}

	for _, emoji := range []string{promptPrevEmoji, promptNextEmoji} {
		if err := e.Client().Rest.AddReaction(message.ChannelID, message.ID, emoji, rest.WithCtx(ctx)); err != nil {
			logger.Error("Failed to add prompt paginator reaction",
				zap.Error(err),
				zap.String("message_id", message.ID.String()),
				zap.String("emoji", emoji))
			return
		}
	}

	startPromptPaginator(e.Client(), message.ChannelID, message.ID, e.User().ID, embeds)
}

func startPromptPaginator(client *bot.Client, channelID, messageID, userID snowflake.ID, embeds []discord.Embed) {
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), promptPaginatorTimeout)
	reactionEvents, stop := bot.NewEventCollector[*events.MessageReactionAdd](client, func(e *events.MessageReactionAdd) bool {
		if e == nil {
			return false
		}

		if e.ChannelID != channelID || e.MessageID != messageID || e.UserID != userID {
			return false
		}

		switch e.Emoji.Reaction() {
		case promptPrevEmoji, promptNextEmoji:
			return true
		default:
			return false
		}
	})

	go func() {
		defer cancel()
		defer stop()
		defer clearPromptPaginatorReactions(client, channelID, messageID)

		currentPage := 0
		for {
			select {
			case <-ctx.Done():
				return
			case reactionEvent, ok := <-reactionEvents:
				if !ok {
					return
				}

				targetPage := currentPage
				switch reactionEvent.Emoji.Reaction() {
				case promptPrevEmoji:
					if targetPage > 0 {
						targetPage--
					}
				case promptNextEmoji:
					if targetPage < len(embeds)-1 {
						targetPage++
					}
				}

				if targetPage != currentPage {
					if _, err := client.Rest.UpdateMessage(
						channelID,
						messageID,
						discord.NewMessageUpdate().WithEmbeds(embeds[targetPage]),
						rest.WithCtx(stdcontext.Background()),
					); err != nil {
						logger.Error("Failed to update prompt paginator page",
							zap.Error(err),
							zap.String("message_id", messageID.String()),
							zap.Int("page", targetPage+1))
					} else {
						currentPage = targetPage
					}
				}

				if err := client.Rest.RemoveUserReaction(
					channelID,
					messageID,
					reactionEvent.Emoji.Reaction(),
					userID,
					rest.WithCtx(stdcontext.Background()),
				); err != nil {
					logger.Debug("Failed to remove prompt paginator reaction",
						zap.Error(err),
						zap.String("message_id", messageID.String()),
						zap.String("emoji", reactionEvent.Emoji.Reaction()))
				}
			}
		}
	}()
}

func clearPromptPaginatorReactions(client *bot.Client, channelID, messageID snowflake.ID) {
	for _, emoji := range []string{promptPrevEmoji, promptNextEmoji} {
		if err := client.Rest.RemoveAllReactionsForEmoji(
			channelID,
			messageID,
			emoji,
			rest.WithCtx(stdcontext.Background()),
		); err != nil {
			logger.Debug("Failed to clear prompt paginator reaction",
				zap.Error(err),
				zap.String("message_id", messageID.String()),
				zap.String("emoji", emoji))
		}
	}
}

func buildPromptEmbeds(title string, prompt string, pages []string) []discord.Embed {
	totalChars := len([]rune(prompt))
	embeds := make([]discord.Embed, 0, len(pages))
	for i, page := range pages {
		embed := discord.NewEmbedBuilder().
			SetTitle(title).
			SetDescription(page).
			SetColor(promptPaginatorColor).
			SetFooter(fmt.Sprintf("Page %d/%d • %d chars • Use reactions to navigate", i+1, len(pages), totalChars), "").
			Build()
		embeds = append(embeds, embed)
	}
	return embeds
}

func splitPromptPages(prompt string, maxRunes int) []string {
	if prompt == "" {
		return []string{"(empty prompt)"}
	}

	runes := []rune(prompt)
	if len(runes) <= maxRunes {
		return []string{prompt}
	}

	pages := make([]string, 0, (len(runes)/maxRunes)+1)
	for start := 0; start < len(runes); {
		remaining := len(runes) - start
		if remaining <= maxRunes {
			pages = append(pages, string(runes[start:]))
			break
		}

		split := findPromptSplit(runes[start:start+maxRunes], maxRunes)
		pages = append(pages, string(runes[start:start+split]))
		start += split
	}

	return pages
}

func findPromptSplit(chunk []rune, maxRunes int) int {
	searchFloor := maxRunes - 200
	if searchFloor < 1 {
		searchFloor = 1
	}

	for i := len(chunk) - 1; i >= searchFloor-1; i-- {
		if chunk[i] == '\n' {
			return i + 1
		}
	}

	for i := len(chunk) - 1; i >= searchFloor-1; i-- {
		if unicode.IsSpace(chunk[i]) {
			return i + 1
		}
	}

	return len(chunk)
}
