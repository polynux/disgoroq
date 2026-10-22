package handlers

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
)

type VoteHandler struct {
	client *bot.Client
	repo   *database.Repository
	config config.VotesConfig
	// recountMu serializes recounts so a slower fetch cannot overwrite newer counts.
	recountMu sync.Mutex
}

func NewVoteHandler(client *bot.Client, repo *database.Repository, cfg config.VotesConfig) *VoteHandler {
	return &VoteHandler{client: client, repo: repo, config: cfg}
}

func (h *VoteHandler) HandleGuildMessageReactionAdd(e *events.GuildMessageReactionAdd) {
	if e.UserID == e.Client().ID() || !h.isVoteEmoji(partialEmojiName(e.Emoji.Name)) {
		return
	}
	h.recount(e.ChannelID, e.MessageID)
}

func (h *VoteHandler) HandleGuildMessageReactionRemove(e *events.GuildMessageReactionRemove) {
	if e.UserID == e.Client().ID() || !h.isVoteEmoji(partialEmojiName(e.Emoji.Name)) {
		return
	}
	h.recount(e.ChannelID, e.MessageID)
}

func (h *VoteHandler) HandleGuildMessageReactionRemoveEmoji(e *events.GuildMessageReactionRemoveEmoji) {
	h.recount(e.ChannelID, e.MessageID)
}

func (h *VoteHandler) HandleGuildMessageReactionRemoveAll(e *events.GuildMessageReactionRemoveAll) {
	h.recount(e.ChannelID, e.MessageID)
}

func (h *VoteHandler) isVoteEmoji(emoji string) bool {
	return normalizeVoteEmoji(emoji) == normalizeVoteEmoji(h.config.GoodEmoji) ||
		normalizeVoteEmoji(emoji) == normalizeVoteEmoji(h.config.BadEmoji)
}

// recount runs off the gateway goroutine: disgo dispatches listeners inline,
// so a slow REST call here would delay every other event.
func (h *VoteHandler) recount(channelID, messageID snowflake.ID) {
	if !h.config.Enabled {
		return
	}
	go h.recountNow(channelID, messageID)
}

func (h *VoteHandler) recountNow(channelID, messageID snowflake.ID) {
	h.recountMu.Lock()
	defer h.recountMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vote, err := h.repo.GetBotReplyVote(ctx, messageID.String())
	if err != nil {
		return
	}
	message, err := h.client.Rest.GetMessage(channelID, messageID, rest.WithCtx(ctx))
	if err != nil {
		logger.Warn("Failed to fetch message for reply vote recount",
			zap.String("message_id", messageID.String()), zap.Error(err))
		return
	}
	goodCount, badCount := ReplyVoteCounts(message.Reactions, h.config.GoodEmoji, h.config.BadEmoji)
	if err := h.repo.UpdateBotReplyVoteCounts(ctx, vote.MessageID, goodCount, badCount); err != nil {
		logger.Warn("Failed to update reply vote counts",
			zap.String("message_id", messageID.String()), zap.Error(err))
	}
}

// ReplyVoteCounts returns user vote counts, excluding the bot's seed reactions.
func ReplyVoteCounts(reactions []discord.MessageReaction, goodEmoji, badEmoji string) (int, int) {
	var goodCount, badCount int
	goodKey, badKey := normalizeVoteEmoji(goodEmoji), normalizeVoteEmoji(badEmoji)
	for _, reaction := range reactions {
		count := reaction.Count
		if reaction.Me {
			count--
		}
		if count < 0 {
			count = 0
		}
		switch normalizeVoteEmoji(reaction.Emoji.Name) {
		case goodKey:
			goodCount += count
		case badKey:
			badCount += count
		}
	}
	return goodCount, badCount
}

func normalizeVoteEmoji(emoji string) string {
	return strings.ReplaceAll(strings.TrimSpace(emoji), "\uFE0F", "")
}

func partialEmojiName(name *string) string {
	if name == nil {
		return ""
	}
	return *name
}

func (h *MessageHandler) addReplyVoteReactions(reply discord.Message, source discord.Message, response *ai.ChatResponse, provider string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Store the record before reacting so the first votes find it.
	if err := h.repo.InsertBotReplyVote(ctx, database.BotReplyVote{
		MessageID: reply.ID.String(), GuildID: source.GuildID.String(), ChannelID: reply.ChannelID.String(),
		Content: response.Content, ReplyToMessageID: source.ID.String(), ReplyToContent: source.Content,
		Provider: provider, Model: response.Model,
	}); err != nil {
		logger.Warn("Failed to store bot reply vote record",
			zap.String("message_id", reply.ID.String()), zap.Error(err))
		return
	}
	for _, emoji := range []string{h.votesConfig.GoodEmoji, h.votesConfig.BadEmoji} {
		if err := h.client.Rest.AddReaction(reply.ChannelID, reply.ID, strings.TrimSpace(emoji), rest.WithCtx(ctx)); err != nil {
			logger.Warn("Failed to add reply vote reaction",
				zap.String("message_id", reply.ID.String()), zap.String("emoji", emoji), zap.Error(err))
		}
	}
}
