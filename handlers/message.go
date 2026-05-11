package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"text/template"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	appcontext "polynux/disgoroq/context"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
	"polynux/disgoroq/triggerwords"
	"polynux/disgoroq/utils"
)

type MessageHandler struct {
	client         *bot.Client
	aiservice      *ai.Service
	repo           *database.Repository
	contextBuilder *ai.ContextBuilder
	memoryService  memory.Service
	emojiManager   *emoji.Manager
	defaultPrompt  string
	triggerWords   []string
}

func NewMessageHandler(client *bot.Client, aiService *ai.Service, repo *database.Repository, memoryService memory.Service, emojiManager *emoji.Manager, defaultPrompt string, triggerWords []string) *MessageHandler {
	return &MessageHandler{
		client:         client,
		aiservice:      aiService,
		repo:           repo,
		contextBuilder: ai.NewContextBuilder(client, aiService),
		memoryService:  memoryService,
		emojiManager:   emojiManager,
		defaultPrompt:  defaultPrompt,
		triggerWords:   triggerwords.NormalizeAll(triggerWords),
	}
}

func (h *MessageHandler) HandleMessageCreate(e *events.MessageCreate) {
	client := e.Client()
	m := e.Message

	h.cacheMessage(context.Background(), m)

	if m.Author.ID == client.ID() {
		return
	}

	if m.GuildID != nil {
		_ = h.repo.SetChannelLastMessage(context.Background(), m.GuildID.String(), m.ChannelID.String(), time.Now().Unix())
	}

	// Buffer ALL messages for memory processing (non-blocking) - do this first!
	if h.memoryService != nil && m.GuildID != nil {
		go func() {
			ctx := context.Background()
			if err := h.memoryService.BufferMessage(ctx, m.Author.ID.String(), m.GuildID.String(), m.Content); err != nil {
				logger.Warn("Failed to buffer message for memory",
					zap.String("user_id", m.Author.ID.String()),
					zap.String("guild_id", m.GuildID.String()),
					zap.Error(err))
			}
		}()
	}

	ctx, cancel := appcontext.Message()
	defer cancel()

	explicitTrigger := h.isExplicitTrigger(ctx, &m, client.ID())

	botMember, err := client.Rest.GetMember(*m.GuildID, client.ID(), rest.WithCtx(ctx))
	if err != nil {
		logger.Error("Failed to get bot member",
			zap.String("guild_id", m.GuildID.String()),
			zap.Error(err))
		return
	}

	threshold := h.repo.GetThreshold(ctx, m.GuildID.String())
	thresholdSexe := h.repo.GetThresholdSexe(ctx, m.GuildID.String())

	randFloat := rand.Float32()
	if randFloat < float32(thresholdSexe) && !explicitTrigger {
		if rand.Float32() < 0.5 {
			_ = h.createMessage(m.ChannelID, discord.MessageCreate{Content: "(et je parle de sexe evidemment)"})
			return
		} else {
			_ = h.createMessage(m.ChannelID, discord.MessageCreate{Content: "malin ça, j'ai la barre maintenant"})
			return
		}
	}

	randFloat = rand.Float32()
	if randFloat > float32(threshold) && !explicitTrigger {
		return
	}

	state := h.repo.GetState(ctx, m.GuildID.String())
	if state == "off" {
		return
	}

	lastMessageTime := h.repo.GetLastMessage(ctx, m.GuildID.String())
	if lastMessageTime > 0 && !explicitTrigger {
		if time.Now().Unix()-lastMessageTime < database.DefaultRateLimit {
			return
		}
	}

	_ = h.repo.SetLastMessage(ctx, m.GuildID.String(), time.Now().Unix())

	client.Rest.SendTyping(m.ChannelID, rest.WithCtx(ctx))

	messageCount := h.repo.GetMessagesCount(ctx, m.GuildID.String())
	messages, err := h.getMessages(ctx, m.ChannelID, m.GuildID.String(), messageCount, &m)
	if err != nil {
		logger.Error("Error getting messages", zap.Error(err))
		return
	}

	buildCtx, buildCancel := appcontext.AI()
	defer buildCancel()

	processedMessage, err := h.contextBuilder.BuildContext(buildCtx, messages, *m.GuildID, client.ID())
	if err != nil {
		logger.Error("Error building context", zap.Error(err))
		return
	}

	temperature := h.repo.GetTemperature(ctx, m.GuildID.String())

	// Build memory context if available
	var memoryContext string
	if h.memoryService != nil {
		memoryCtx, err := h.memoryService.GetMemoryContext(ctx, m.Author.ID.String(), m.GuildID.String(), m.Content)
		if err == nil && memoryCtx != nil && len(memoryCtx.Summaries) > 0 {
			memoryContext = "\n\n**Contexte de conversation:**\n"
			for i, summary := range memoryCtx.Summaries {
				if i < 2 { // Limit to top 2 summaries
					memoryContext += fmt.Sprintf("- %s\n", summary.Content)
				}
			}
			if memoryCtx.ConfidenceScore > 0.7 {
				memoryContext += "(contexte pertinent pour cette conversation)"
			}
			// Log to console only in debug mode
			if logger.IsDebugMode() {
				logger.Debug("Using memory summaries for response",
					zap.String("user_id", m.Author.ID.String()),
					zap.String("guild_id", m.GuildID.String()),
					zap.Int("summary_count", len(memoryCtx.Summaries)),
					zap.Float64("confidence", memoryCtx.ConfidenceScore))
			}
		} else if logger.IsDebugMode() {
			logger.Debug("No memory summaries available for response",
				zap.String("user_id", m.Author.ID.String()),
				zap.String("guild_id", m.GuildID.String()))
		}
	}

	botNick := ""
	if botMember.Nick != nil {
		botNick = *botMember.Nick
	}
	if botNick == "" {
		botNick = botMember.User.Username
	}

	instructions := h.GetDefaultPrompt(botNick)
	instructions += memoryContext

	if prompt, ok := h.repo.GetPrompt(ctx, m.GuildID.String()); ok {
		instructions = prompt
	}
	if h.emojiManager != nil {
		instructions += h.emojiManager.GuildEmojiPrompt(m.GuildID.String(), 50)
	}

	client.Rest.SendTyping(m.ChannelID, rest.WithCtx(ctx))

	// Start AI call with comprehensive logging and retry/fallback support
	logger.Debug("Starting AI chat request",
		zap.String("guild_id", m.GuildID.String()),
		zap.String("channel_id", m.ChannelID.String()),
		zap.String("user_id", m.Author.ID.String()),
		zap.Int("message_count", len(processedMessage.Messages)),
		zap.Int("image_count", len(processedMessage.Images)))

	chatCtx, chatCancel := appcontext.AI()
	defer chatCancel()

	response, err := h.aiservice.Chat(chatCtx, &ai.ChatRequest{
		SystemPrompt: instructions,
		Messages:     processedMessage.Messages,
		Images:       processedMessage.Images,
		Temperature:  temperature,
		MaxTokens:    database.DefaultMaxTokens,
	})

	reference := &discord.MessageReference{
		MessageID: &m.ID,
		ChannelID: &m.ChannelID,
		GuildID:   m.GuildID,
	}

	if err != nil {
		// Enhanced error handling with user-friendly messages
		logger.Error("AI chat failed after all retries and fallbacks",
			zap.Error(err),
			zap.String("guild_id", m.GuildID.String()),
			zap.String("user_id", m.Author.ID.String()))

		// Send user-friendly error message
		errorMsg := "Désolé, j'ai des soucis techniques là... 🤖💀"
		if h.aiservice.IsFallbackAvailable() {
			errorMsg = "Désolé, tous mes systèmes sont en rade... 🤖💀"
		}

		_ = h.createMessage(m.ChannelID, discord.MessageCreate{
			Content:          errorMsg,
			MessageReference: reference,
		})
		return
	}

	// Validate response before sending
	if response.Content == "" {
		logger.Error("Received empty response from AI service",
			zap.String("guild_id", m.GuildID.String()),
			zap.String("user_id", m.Author.ID.String()))

		_ = h.createMessage(m.ChannelID, discord.MessageCreate{
			Content:          "Euh... j'ai perdu mes mots là 😅",
			MessageReference: reference,
		})
		return
	}

	response.Content = utils.NormalizeBotText(response.Content)

	// Convert emoji shortcodes to Discord format (if emoji manager is available)
	if h.emojiManager != nil {
		response.Content = h.emojiManager.ConvertShortcodesToDiscordEmojis(response.Content, m.GuildID.String())
	}

	if response.Content == "" {
		logger.Error("AI response became empty after post-processing",
			zap.String("guild_id", m.GuildID.String()),
			zap.String("user_id", m.Author.ID.String()))
		_ = h.createMessage(m.ChannelID, discord.MessageCreate{
			Content:          "Euh... j'ai perdu mes mots là 😅",
			MessageReference: reference,
		})
		return
	}

	providerName := response.Provider
	if providerName == "" {
		providerName = h.aiservice.Name()
	}

	// Log successful response after post-processing so the payload reflects what is sent.
	logger.Info("AI response successful",
		zap.String("guild_id", m.GuildID.String()),
		zap.String("user_id", m.Author.ID.String()),
		zap.String("provider", providerName),
		zap.Int("response_length", len(response.Content)),
		zap.Int("tokens_used", response.TokensUsed))

	if err := h.createMessage(m.ChannelID, discord.MessageCreate{
		Content:          response.Content,
		MessageReference: reference,
		AllowedMentions:  &discord.AllowedMentions{Parse: []discord.AllowedMentionType{}},
	}); err != nil {
		logger.Error("Failed to send AI response",
			zap.String("guild_id", m.GuildID.String()),
			zap.String("user_id", m.Author.ID.String()),
			zap.String("provider", providerName),
			zap.Error(err))
	}
}

func (h *MessageHandler) HandleMessageUpdate(e *events.MessageUpdate) {
	h.cacheMessage(context.Background(), normalizeEventMessage(e.GenericMessage))
}

func (h *MessageHandler) HandleMessageDelete(e *events.MessageDelete) {
	guildID := ""
	if e.GuildID != nil {
		guildID = e.GuildID.String()
	}

	if err := h.repo.MarkDiscordMessageDeleted(
		context.Background(),
		e.MessageID.String(),
		e.ChannelID.String(),
		guildID,
		time.Now(),
	); err != nil {
		logger.Warn("Failed to tombstone deleted Discord message",
			zap.String("message_id", e.MessageID.String()),
			zap.String("channel_id", e.ChannelID.String()),
			zap.String("guild_id", guildID),
			zap.Error(err))
	}
}

func (h *MessageHandler) createMessage(channelID snowflake.ID, message discord.MessageCreate) error {
	sendCtx, sendCancel := appcontext.Message()
	defer sendCancel()

	createdMessage, err := h.client.Rest.CreateMessage(channelID, message, rest.WithCtx(sendCtx))
	if err != nil {
		return err
	}

	if createdMessage != nil {
		h.cacheMessage(context.Background(), *createdMessage)
	}
	return err
}

func (h *MessageHandler) botMentioned(m *discord.Message, botID snowflake.ID) bool {
	for _, mention := range m.Mentions {
		if mention.ID == botID {
			return true
		}
	}
	return false
}

func (h *MessageHandler) isExplicitTrigger(ctx context.Context, m *discord.Message, botID snowflake.ID) bool {
	if h.botMentioned(m, botID) {
		return true
	}

	if m.GuildID == nil {
		return false
	}

	triggerWords := h.triggerWords
	if customTriggerWords, ok := h.repo.GetTriggerWords(ctx, m.GuildID.String()); ok {
		triggerWords = customTriggerWords
	}

	return triggerwords.Contains(m.Content, triggerWords)
}

func (h *MessageHandler) getMessages(ctx context.Context, channelID snowflake.ID, guildID string, num int, latest *discord.Message) ([]discord.Message, error) {
	cachedMessages, err := h.repo.GetRecentDiscordMessagesByChannel(ctx, channelID.String(), num)
	if err == nil {
		cachedMessages = ensureLatestMessage(cachedMessages, latest, num)
		if len(cachedMessages) >= num || h.repo.IsDiscordMessageHistoryExhausted(ctx, channelID.String()) {
			return cachedMessages, nil
		}
	} else {
		logger.Warn("Failed to read cached Discord messages",
			zap.String("channel_id", channelID.String()),
			zap.Error(err))
	}

	messages, exhausted, err := h.fetchMessagesFromDiscord(ctx, channelID, num)
	if err != nil {
		return nil, err
	}
	if err := h.repo.CacheDiscordMessages(ctx, messages); err != nil {
		logger.Warn("Failed to backfill Discord messages into cache",
			zap.String("channel_id", channelID.String()),
			zap.Error(err))
	}
	if err := h.repo.SetDiscordMessageHistoryExhausted(ctx, channelID.String(), guildID, exhausted); err != nil {
		logger.Warn("Failed to update Discord message cache state",
			zap.String("channel_id", channelID.String()),
			zap.Error(err))
	}
	return ensureLatestMessage(messages, latest, num), nil
}

func (h *MessageHandler) fetchMessagesFromDiscord(ctx context.Context, channelID snowflake.ID, num int) ([]discord.Message, bool, error) {
	if num <= 100 {
		messages, err := h.client.Rest.GetMessages(channelID, 0, 0, 0, num, rest.WithCtx(ctx))
		if err != nil {
			return nil, false, err
		}
		return messages, len(messages) < num, nil
	}

	messages := []discord.Message{}
	historyExhausted := false
	for num > 0 {
		toGet := min(100, num)
		var before snowflake.ID
		if len(messages) > 0 {
			before = messages[len(messages)-1].ID
		}
		newMessages, err := h.client.Rest.GetMessages(channelID, 0, before, 0, toGet, rest.WithCtx(ctx))
		if err != nil {
			return nil, false, err
		}
		messages = append(messages, newMessages...)
		if len(newMessages) < toGet {
			historyExhausted = true
			break
		}
		num -= toGet
	}
	return messages, historyExhausted, nil
}

func (h *MessageHandler) cacheMessage(ctx context.Context, message discord.Message) {
	if message.ID == 0 || message.ChannelID == 0 {
		return
	}

	if err := h.repo.CacheDiscordMessage(ctx, message); err != nil {
		guildID := ""
		if message.GuildID != nil {
			guildID = message.GuildID.String()
		}
		logger.Warn("Failed to cache Discord message",
			zap.String("message_id", message.ID.String()),
			zap.String("channel_id", message.ChannelID.String()),
			zap.String("guild_id", guildID),
			zap.Error(err))
	}
}

func ensureLatestMessage(messages []discord.Message, latest *discord.Message, limit int) []discord.Message {
	if latest == nil || latest.ID == 0 {
		return messages
	}
	if len(messages) > 0 && messages[0].ID == latest.ID {
		return messages
	}

	result := make([]discord.Message, 0, min(limit, len(messages)+1))
	result = append(result, *latest)
	for _, message := range messages {
		if message.ID == latest.ID {
			continue
		}
		if len(result) >= limit {
			break
		}
		result = append(result, message)
	}
	return result
}

func normalizeEventMessage(event *events.GenericMessage) discord.Message {
	message := event.Message
	if message.ID == 0 {
		message.ID = event.MessageID
	}
	if message.ChannelID == 0 {
		message.ChannelID = event.ChannelID
	}
	if message.GuildID == nil && event.GuildID != nil {
		guildID := *event.GuildID
		message.GuildID = &guildID
	}
	if message.CreatedAt.IsZero() && message.ID != 0 {
		message.CreatedAt = message.ID.Time()
	}
	return message
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *MessageHandler) GetDefaultPrompt(botNick string) string {
	tmpl, err := template.New("prompt").Parse(h.defaultPrompt)
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
