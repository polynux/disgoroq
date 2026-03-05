package reengage

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
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
)

type Service struct {
	client         *bot.Client
	aiService      *ai.Service
	repo           *database.Repository
	contextBuilder *ai.ContextBuilder
	memoryService  memory.Service
	emojiManager   *emoji.Manager
	config         config.ReengageConfig
	defaultPrompt  string
}

func NewService(client *bot.Client, aiService *ai.Service, repo *database.Repository, memoryService memory.Service, emojiManager *emoji.Manager, cfg config.ReengageConfig, defaultPrompt string) *Service {
	return &Service{
		client:         client,
		aiService:      aiService,
		repo:           repo,
		contextBuilder: ai.NewContextBuilder(client, aiService),
		memoryService:  memoryService,
		emojiManager:   emojiManager,
		config:         cfg,
		defaultPrompt:  defaultPrompt,
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

		// Use REST API instead of state
		_, err = s.client.Rest.GetGuild(guildIDSnowflake, false, rest.WithCtx(ctx))
		if err != nil {
			logger.Warn("Failed to get guild", zap.String("guild_id", guildID), zap.Error(err))
			continue
		}

		channels, err := s.client.Rest.GetGuildChannels(guildIDSnowflake, rest.WithCtx(ctx))
		if err != nil {
			logger.Warn("Failed to get guild channels", zap.String("guild_id", guildID), zap.Error(err))
			continue
		}

		for _, channel := range channels {
			if channel.Type() != discord.ChannelTypeGuildText {
				continue
			}

			enabled := s.repo.GetReengageEnabled(ctx, guildID, channel.ID().String())
			if !enabled {
				continue
			}

			lastMessageTimestamp := s.repo.GetChannelLastMessage(ctx, guildID, channel.ID().String())
			thresholdMinutes := s.repo.GetReengageThreshold(ctx, guildID, channel.ID().String())
			chance := s.repo.GetReengageChance(ctx, guildID, channel.ID().String())

			if lastMessageTimestamp == 0 {
				continue
			}

			lastMessageTime := time.Unix(lastMessageTimestamp, 0)
			timeSinceLastMessage := time.Since(lastMessageTime)
			thresholdDuration := time.Duration(thresholdMinutes) * time.Minute

			if timeSinceLastMessage <= thresholdDuration {
				continue
			}

			if rand.Float64() >= chance {
				continue
			}

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

func (s *Service) GenerateAndSend(ctx context.Context, guildID, channelID string) error {
	channelIDSnowflake := snowflake.MustParse(channelID)

	messages, err := s.client.Rest.GetMessages(channelIDSnowflake, 0, 0, 0, 20, rest.WithCtx(ctx))
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

	reengageMessage := s.config.ReengageMessage
	if msg, ok := s.repo.GetReengageMessage(ctx, guildID); ok {
		reengageMessage = msg
	}

	var systemPrompt string
	if prompt, ok := s.repo.GetPrompt(ctx, guildID); ok {
		systemPrompt = prompt + reengageMessage
	} else {
		systemPrompt = s.getDefaultPrompt(botNick) + reengageMessage
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

	response, err := s.aiService.Chat(ctx, &ai.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     processedMessage.Messages,
		Images:       processedMessage.Images,
		Temperature:  database.DefaultTemperature,
		MaxTokens:    database.DefaultMaxTokens,
	})
	if err != nil {
		return fmt.Errorf("failed to generate AI response: %w", err)
	}

	if response.Content == "" {
		return fmt.Errorf("received empty response from AI service")
	}

	content := response.Content
	if s.emojiManager != nil {
		content = s.emojiManager.ConvertShortcodesToDiscordEmojis(content, guildID)
	}

	_, err = s.client.Rest.CreateMessage(channelIDSnowflake, discord.MessageCreate{Content: content}, rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	_ = s.repo.SetChannelLastMessage(ctx, guildID, channelID, time.Now().Unix())

	logger.Info("Reengage message sent successfully",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID),
		zap.Int("response_length", len(response.Content)),
	)

	return nil
}

func (s *Service) getDefaultPrompt(botNick string) string {
	tmpl, err := template.New("prompt").Parse(s.defaultPrompt)
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
