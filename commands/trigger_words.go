package commands

import (
	stdcontext "context"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"polynux/disgoroq/database"
	"polynux/disgoroq/triggerwords"
)

func triggerWordsHandler(repo *database.Repository) func(e *events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		guildID := getGuildID(e)
		if guildID == "" {
			_ = e.CreateMessage(discord.MessageCreate{Content: "This command can only be used in a server."})
			return
		}

		data := e.SlashCommandInteractionData()
		subcommandName := ""
		if data.SubCommandName != nil {
			subcommandName = *data.SubCommandName
		}

		switch subcommandName {
		case "list":
			handleTriggerWordsList(e, repo, guildID)
		case "add":
			handleTriggerWordsAdd(e, repo, guildID)
		case "remove":
			handleTriggerWordsRemove(e, repo, guildID)
		case "set":
			handleTriggerWordsSet(e, repo, guildID)
		case "reset":
			handleTriggerWordsReset(e, repo, guildID)
		default:
			_ = e.CreateMessage(discord.MessageCreate{Content: "Unknown subcommand!"})
		}
	}
}

func handleTriggerWordsList(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	ctx := stdcontext.Background()
	triggerWords, hasCustom := effectiveTriggerWords(ctx, repo, guildID)
	source := "default"
	if hasCustom {
		source = "custom"
	}

	if len(triggerWords) == 0 {
		_ = e.CreateMessage(discord.MessageCreate{
			Content: fmt.Sprintf("No trigger words configured for this server (%s).", source),
		})
		return
	}

	_ = e.CreateMessage(discord.MessageCreate{
		Content: fmt.Sprintf("Trigger words (%s): `%s`", source, strings.Join(triggerWords, "`, `")),
	})
}

func handleTriggerWordsAdd(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	ctx := stdcontext.Background()
	word := triggerwords.Normalize(e.SlashCommandInteractionData().String("word"))
	if err := triggerwords.Validate([]string{word}); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error adding trigger word: " + err.Error()})
		return
	}

	triggerWords, _ := effectiveTriggerWords(ctx, repo, guildID)
	if slices.Contains(triggerWords, word) {
		_ = e.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("Trigger word `%s` is already configured.", word)})
		return
	}

	triggerWords = append(triggerWords, word)
	if err := saveTriggerWords(ctx, repo, guildID, triggerWords); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error adding trigger word: " + err.Error()})
		return
	}

	_ = e.CreateMessage(discord.MessageCreate{
		Content: fmt.Sprintf("✅ Trigger word added. Current words: `%s`", strings.Join(triggerwords.NormalizeAll(triggerWords), "`, `")),
	})
}

func handleTriggerWordsRemove(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	ctx := stdcontext.Background()
	word := triggerwords.Normalize(e.SlashCommandInteractionData().String("word"))
	if err := triggerwords.Validate([]string{word}); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error removing trigger word: " + err.Error()})
		return
	}

	triggerWords, _ := effectiveTriggerWords(ctx, repo, guildID)
	filtered := make([]string, 0, len(triggerWords))
	removed := false
	for _, existingWord := range triggerWords {
		if existingWord == word {
			removed = true
			continue
		}
		filtered = append(filtered, existingWord)
	}

	if !removed {
		_ = e.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("Trigger word `%s` is not configured.", word)})
		return
	}

	if err := saveTriggerWords(ctx, repo, guildID, filtered); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error removing trigger word: " + err.Error()})
		return
	}

	if len(filtered) == 0 {
		_ = e.CreateMessage(discord.MessageCreate{Content: "✅ Trigger word removed. This server now has no trigger words configured."})
		return
	}

	_ = e.CreateMessage(discord.MessageCreate{
		Content: fmt.Sprintf("✅ Trigger word removed. Current words: `%s`", strings.Join(filtered, "`, `")),
	})
}

func handleTriggerWordsSet(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	ctx := stdcontext.Background()
	words := triggerwords.Parse(e.SlashCommandInteractionData().String("words"))
	if len(words) == 0 {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error setting trigger words: provide at least one trigger word."})
		return
	}
	if err := triggerwords.Validate(words); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error setting trigger words: " + err.Error()})
		return
	}

	if err := saveTriggerWords(ctx, repo, guildID, words); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error setting trigger words: " + err.Error()})
		return
	}

	_ = e.CreateMessage(discord.MessageCreate{
		Content: fmt.Sprintf("✅ Trigger words set to: `%s`", strings.Join(words, "`, `")),
	})
}

func handleTriggerWordsReset(e *events.ApplicationCommandInteractionCreate, repo *database.Repository, guildID string) {
	ctx := stdcontext.Background()
	if err := repo.DeleteTriggerWords(ctx, guildID); err != nil {
		_ = e.CreateMessage(discord.MessageCreate{Content: "Error resetting trigger words: " + err.Error()})
		return
	}

	if len(defaultTriggerWords) == 0 {
		_ = e.CreateMessage(discord.MessageCreate{Content: "✅ Trigger words reset. No default trigger words are configured."})
		return
	}

	_ = e.CreateMessage(discord.MessageCreate{
		Content: fmt.Sprintf("✅ Trigger words reset to defaults: `%s`", strings.Join(defaultTriggerWords, "`, `")),
	})
}

func effectiveTriggerWords(ctx stdcontext.Context, repo *database.Repository, guildID string) ([]string, bool) {
	if triggerWords, ok := repo.GetTriggerWords(ctx, guildID); ok {
		return triggerWords, true
	}
	return append([]string(nil), defaultTriggerWords...), false
}

func saveTriggerWords(ctx stdcontext.Context, repo *database.Repository, guildID string, words []string) error {
	normalized := triggerwords.NormalizeAll(words)
	if slices.Equal(normalized, defaultTriggerWords) {
		return repo.DeleteTriggerWords(ctx, guildID)
	}
	return repo.SetTriggerWords(ctx, guildID, normalized)
}
