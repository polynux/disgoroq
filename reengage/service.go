package reengage

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
	"polynux/disgoroq/utils"
)

type Service struct {
	client         *bot.Client
	aiService      *ai.Service
	repo           *database.Repository
	contextBuilder *ai.ContextBuilder
	memoryService  memory.Service
	emojiManager   *emoji.Manager
	runtime        *config.Runtime
	skippedMu      sync.Mutex
	skippedGuilds  map[string]struct{}
}

func (s *Service) reengageConfig() config.ReengageConfig {
	return s.runtime.Current().Reengage
}

func (s *Service) currentDefaultPrompt() string {
	return s.runtime.Current().Bot.DefaultPrompt
}

func (s *Service) resolveReengageSettings(ctx context.Context, guildID, channelID string) (bool, int, float64) {
	cfg := s.reengageConfig()
	enabled := s.repo.GetReengageEnabled(ctx, guildID, channelID)
	thresholdMinutes, ok := s.repo.GetReengageThreshold(ctx, guildID, channelID)
	if !ok {
		thresholdMinutes = cfg.DefaultInactivityMinutes
	}

	chance, ok := s.repo.GetReengageChance(ctx, guildID, channelID)
	if !ok {
		chance = cfg.DefaultChance
	}

	return enabled, thresholdMinutes, chance
}

func (s *Service) shouldReengage(lastMessageTimestamp int64, thresholdMinutes int, chance float64, roll float64, now time.Time) bool {
	if lastMessageTimestamp == 0 {
		return false
	}

	lastMessageTime := time.Unix(lastMessageTimestamp, 0)
	if now.Sub(lastMessageTime) <= time.Duration(thresholdMinutes)*time.Minute {
		return false
	}

	return roll < chance
}

func NewService(client *bot.Client, aiService *ai.Service, repo *database.Repository, memoryService memory.Service, emojiManager *emoji.Manager, cfgRuntime *config.Runtime) *Service {
	return &Service{
		client:         client,
		aiService:      aiService,
		repo:           repo,
		contextBuilder: ai.NewContextBuilder(client, aiService),
		memoryService:  memoryService,
		emojiManager:   emojiManager,
		runtime:        cfgRuntime,
		skippedGuilds:  make(map[string]struct{}),
	}
}

func (s *Service) CheckAllChannels(ctx context.Context) error {
	guilds, err := s.repo.GetAllGuilds(ctx)
	if err != nil {
		logger.Error("Failed to get all guilds", zap.Error(err))
		return fmt.Errorf("failed to get all guilds: %w", err)
	}

	for _, guildID := range guilds {
		guildIDSnowflake, err := snowflake.Parse(guildID)
		if err != nil {
			continue
		}

		// The gateway guild cache is authoritative for membership once populated, so
		// guilds the bot left are skipped without any REST call. During an outage
		// disgo moves guilds to the unavailable set, which can empty Guilds; keep
		// trusting the cache then rather than calling REST for every guild.
		if s.client.Caches.GuildsLen() > 0 || len(s.client.Caches.UnavailableGuildIDs()) > 0 {
			if !s.guildInCache(guildIDSnowflake) {
				continue
			}
			s.unskipGuild(guildID)
		} else if s.isGuildSkipped(guildID) {
			continue
		} else if _, err = s.client.Rest.GetGuild(guildIDSnowflake, false, rest.WithCtx(ctx)); err != nil {
			if s.skipUnknownGuild(err, guildID) {
				continue
			}
			logger.Warn("Failed to get guild", zap.String("guild_id", guildID), zap.Error(err))
			continue
		}

		channels, err := s.client.Rest.GetGuildChannels(guildIDSnowflake, rest.WithCtx(ctx))
		if err != nil {
			if s.skipUnknownGuild(err, guildID) {
				continue
			}
			logger.Warn("Failed to get guild channels", zap.String("guild_id", guildID), zap.Error(err))
			continue
		}

		for _, channel := range channels {
			if channel.Type() != discord.ChannelTypeGuildText {
				continue
			}

			enabled, thresholdMinutes, chance := s.resolveReengageSettings(ctx, guildID, channel.ID().String())
			if !enabled {
				continue
			}

			lastMessageTimestamp := s.repo.GetChannelLastMessage(ctx, guildID, channel.ID().String())

			if lastMessageTimestamp == 0 {
				continue
			}

			now := time.Now()
			if !s.shouldReengage(lastMessageTimestamp, thresholdMinutes, chance, rand.Float64(), now) {
				continue
			}

			lastMessageTime := time.Unix(lastMessageTimestamp, 0)
			timeSinceLastMessage := now.Sub(lastMessageTime)

			logger.Info("Reengage triggered",
				zap.String("guild_id", guildID),
				zap.String("channel_id", channel.ID().String()),
				zap.Duration("inactive_duration", timeSinceLastMessage),
				zap.Float64("chance", chance),
			)

			if err := s.GenerateAndSend(ctx, guildID, channel.ID().String()); err != nil {
				logger.Error("Failed to generate and send reengage message",
					zap.String("guild_id", guildID),
					zap.String("channel_id", channel.ID().String()),
					zap.Error(err),
				)
			}
		}
	}

	return nil
}

// guildInCache reports whether the bot is currently in the guild according to the
// gateway guild cache.
func (s *Service) guildInCache(guildID snowflake.ID) bool {
	_, ok := s.client.Caches.Guild(guildID)
	return ok
}

// isGuildSkipped reports whether the guild was already found to be left by the bot.
func (s *Service) isGuildSkipped(guildID string) bool {
	s.skippedMu.Lock()
	defer s.skippedMu.Unlock()
	_, ok := s.skippedGuilds[guildID]
	return ok
}

// unskipGuild clears a previous skip, e.g. after the bot was re-invited.
func (s *Service) unskipGuild(guildID string) {
	s.skippedMu.Lock()
	defer s.skippedMu.Unlock()
	delete(s.skippedGuilds, guildID)
}

// skipUnknownGuild stops checking a guild Discord reports as unknown (error 10004)
// and reports whether the error was that case.
func (s *Service) skipUnknownGuild(err error, guildID string) bool {
	if !rest.IsJSONErrorCode(err, rest.JSONErrorCodeUnknownGuild) {
		return false
	}

	// A guild the bot was removed from while disconnected never gets a
	// GUILD_DELETE, so it can linger in the gateway cache. Evict it, or the
	// cache check would keep sending us back to REST every run.
	if s.client != nil {
		if id, parseErr := snowflake.Parse(guildID); parseErr == nil {
			s.client.Caches.RemoveGuild(id)
		}
	}

	s.skippedMu.Lock()
	if s.skippedGuilds == nil {
		s.skippedGuilds = make(map[string]struct{})
	}
	_, already := s.skippedGuilds[guildID]
	s.skippedGuilds[guildID] = struct{}{}
	s.skippedMu.Unlock()

	if !already {
		logger.Info("Skipping guild the bot is no longer in", zap.String("guild_id", guildID))
	}
	return true
}

func (s *Service) GenerateAndSend(ctx context.Context, guildID, channelID string) error {
	channelIDSnowflake := snowflake.MustParse(channelID)

	messages, err := s.getRecentMessages(ctx, guildID, channelIDSnowflake, 20)
	if err != nil {
		return fmt.Errorf("failed to get channel messages: %w", err)
	}

	if len(messages) == 0 {
		return nil
	}

	guildIDSnowflake := snowflake.MustParse(guildID)
	botMember, err := s.client.Rest.GetMember(guildIDSnowflake, s.client.ID(), rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("failed to get bot member: %w", err)
	}

	botNick := ""
	if botMember.Nick != nil {
		botNick = *botMember.Nick
	}
	if botNick == "" {
		botNick = botMember.User.Username
	}

	processedMessage, err := s.contextBuilder.BuildContext(ctx, messages, guildIDSnowflake, s.client.ID())
	if err != nil {
		return fmt.Errorf("failed to build context: %w", err)
	}

	reengageMessage := s.reengageConfig().ReengageMessage
	if msg, ok := s.repo.GetReengageMessage(ctx, guildID); ok {
		reengageMessage = msg
	}

	var systemPrompt string
	if prompt, ok := s.repo.GetPrompt(ctx, guildID); ok {
		systemPrompt = prompt + reengageMessage
	} else {
		systemPrompt = s.getDefaultPrompt(botNick) + reengageMessage
	}
	if s.emojiManager != nil {
		systemPrompt += s.emojiManager.GuildEmojiPrompt(guildID, 50)
	}

	if s.memoryService != nil {
		lastMessage := messages[len(messages)-1]
		memoryCtx, err := s.memoryService.GetMemoryContext(ctx, lastMessage.Author.ID.String(), guildID, lastMessage.Content)
		if err == nil && memoryCtx != nil && len(memoryCtx.Summaries) > 0 {
			memoryContext := "\n\n**Contexte de conversation:**\n"
			for i, summary := range memoryCtx.Summaries {
				if i < 2 {
					memoryContext += fmt.Sprintf("- %s\n", summary.Content)
				}
			}
			if memoryCtx.ConfidenceScore > 0.7 {
				memoryContext += "(contexte pertinent pour cette conversation)"
			}
			systemPrompt += memoryContext
		}
	}

	temperature := s.repo.GetTemperature(ctx, guildID)

	response, err := s.aiService.Chat(ctx, &ai.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     processedMessage.Messages,
		Images:       processedMessage.Images,
		Temperature:  temperature,
		MaxTokens:    database.DefaultMaxTokens,
		BotName:      botNick,
		BotID:        s.client.ID().String(),
		GuildID:      guildID,
		ChannelID:    channelID,
	})
	if err != nil {
		return fmt.Errorf("failed to generate AI response: %w", err)
	}

	if response.Content == "" {
		return fmt.Errorf("received empty response from AI service")
	}

	content := response.Content
	if response.MediaURL == "" {
		content = utils.NormalizeBotText(content)
		if s.emojiManager != nil {
			content = s.emojiManager.ConvertShortcodesToDiscordEmojis(content, guildID)
		}
	} else {
		textPart := content
		if content == response.MediaURL {
			textPart = ""
		} else {
			textPart = strings.TrimSuffix(content, "\n"+response.MediaURL)
		}
		textPart = utils.NormalizeBotText(textPart)
		if s.emojiManager != nil && textPart != "" {
			textPart = s.emojiManager.ConvertShortcodesToDiscordEmojis(textPart, guildID)
		}
		content = ai.AssembleMediaReply(textPart, response.MediaURL)
	}

	createdMessage, err := s.client.Rest.CreateMessage(channelIDSnowflake, discord.MessageCreate{
		Content:         content,
		AllowedMentions: &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}},
	}, rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	if createdMessage != nil {
		if err := s.repo.CacheDiscordMessage(context.Background(), *createdMessage); err != nil {
			logger.Warn("Failed to cache sent reengage message",
				zap.String("guild_id", guildID),
				zap.String("channel_id", channelID),
				zap.String("message_id", createdMessage.ID.String()),
				zap.Error(err))
		}
	}

	_ = s.repo.SetChannelLastMessage(ctx, guildID, channelID, time.Now().Unix())

	logger.Info("Reengage message sent successfully",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID),
		zap.Int("response_length", len(response.Content)),
	)

	return nil
}

func (s *Service) getRecentMessages(ctx context.Context, guildID string, channelID snowflake.ID, limit int) ([]discord.Message, error) {
	messages, err := s.repo.GetRecentDiscordMessagesByChannel(ctx, channelID.String(), limit)
	if err == nil && (len(messages) >= limit || s.repo.IsDiscordMessageHistoryExhausted(ctx, channelID.String())) {
		return messages, nil
	}
	if err != nil {
		logger.Warn("Failed to read cached reengage messages",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID.String()),
			zap.Error(err))
	}

	messages, err = s.client.Rest.GetMessages(channelID, 0, 0, 0, limit, rest.WithCtx(ctx))
	if err != nil {
		return nil, err
	}
	if err := s.repo.CacheDiscordMessages(ctx, messages); err != nil {
		logger.Warn("Failed to backfill reengage messages into cache",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID.String()),
			zap.Error(err))
	}
	if err := s.repo.SetDiscordMessageHistoryExhausted(ctx, channelID.String(), guildID, len(messages) < limit); err != nil {
		logger.Warn("Failed to update reengage message cache state",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID.String()),
			zap.Error(err))
	}
	return messages, nil
}

func (s *Service) getDefaultPrompt(botNick string) string {
	tmpl, err := template.New("prompt").Parse(s.currentDefaultPrompt())
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
