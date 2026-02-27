package reengage

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/handlers"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
)

type Service struct {
	session        *discordgo.Session
	aiService      *ai.Service
	repo           *database.Repository
	contextBuilder *ai.ContextBuilder
	memoryService  memory.Service
	emojiManager   *emoji.Manager
	config         config.ReengageConfig
}

func NewService(session *discordgo.Session, aiService *ai.Service, repo *database.Repository, memoryService memory.Service, emojiManager *emoji.Manager, cfg config.ReengageConfig) *Service {
	return &Service{
		session:        session,
		aiService:      aiService,
		repo:           repo,
		contextBuilder: ai.NewContextBuilder(session, aiService),
		memoryService:  memoryService,
		emojiManager:   emojiManager,
		config:         cfg,
	}
}

func (s *Service) CheckAllChannels(ctx context.Context) error {
	guilds, err := s.repo.GetAllGuilds(ctx)
	if err != nil {
		logger.Error("Failed to get all guilds", zap.Error(err))
		return fmt.Errorf("failed to get all guilds: %w", err)
	}

	for _, guildID := range guilds {
		guild, err := s.session.State.Guild(guildID)
		if err != nil {
			logger.Warn("Failed to get guild state", zap.String("guild_id", guildID), zap.Error(err))
			continue
		}

		for _, channel := range guild.Channels {
			if channel.Type != discordgo.ChannelTypeGuildText {
				continue
			}

			enabled := s.repo.GetReengageEnabled(ctx, guildID, channel.ID)
			if !enabled {
				continue
			}

			lastMessageTimestamp := s.repo.GetChannelLastMessage(ctx, guildID, channel.ID)
			thresholdMinutes := s.repo.GetReengageThreshold(ctx, guildID, channel.ID)
			chance := s.repo.GetReengageChance(ctx, guildID, channel.ID)

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
				zap.String("channel_id", channel.ID),
				zap.Duration("inactive_duration", timeSinceLastMessage),
				zap.Float64("chance", chance),
			)

			if err := s.GenerateAndSend(ctx, guildID, channel.ID); err != nil {
				logger.Error("Failed to generate and send reengage message",
					zap.String("guild_id", guildID),
					zap.String("channel_id", channel.ID),
					zap.Error(err),
				)
			}
		}
	}

	return nil
}

func (s *Service) GenerateAndSend(ctx context.Context, guildID, channelID string) error {
	messages, err := s.session.ChannelMessages(channelID, 20, "", "", "")
	if err != nil {
		return fmt.Errorf("failed to get channel messages: %w", err)
	}

	if len(messages) == 0 {
		return nil
	}

	botMember, err := s.session.GuildMember(guildID, s.session.State.User.ID)
	if err != nil {
		return fmt.Errorf("failed to get bot member: %w", err)
	}

	botNick := botMember.Nick
	if botNick == "" {
		botNick = s.session.State.User.Username
	}

	processedMessage, err := s.contextBuilder.BuildContext(ctx, messages, guildID, s.session.State.User.ID)
	if err != nil {
		return fmt.Errorf("failed to build context: %w", err)
	}

	var systemPrompt string
	if prompt, ok := s.repo.GetPrompt(ctx, guildID); ok {
		systemPrompt = prompt + s.config.ReengageMessage
	} else {
		systemPrompt = handlers.GetDefaultPrompt(botNick) + s.config.ReengageMessage
	}

	if s.memoryService != nil {
		lastMessage := messages[len(messages)-1]
		memoryCtx, err := s.memoryService.GetMemoryContext(ctx, lastMessage.Author.ID, guildID, lastMessage.Content)
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

	_, err = s.session.ChannelMessageSend(channelID, content)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	err = s.repo.SetChannelLastMessage(ctx, guildID, channelID, time.Now().Unix())
	if err != nil {
		logger.Warn("Failed to update channel last message timestamp",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID),
			zap.Error(err),
		)
	}

	logger.Info("Reengage message sent successfully",
		zap.String("guild_id", guildID),
		zap.String("channel_id", channelID),
		zap.Int("response_length", len(response.Content)),
	)

	return nil
}
