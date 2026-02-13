package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
)

type MessageHandler struct {
	session        *discordgo.Session
	aiservice      *ai.Service
	repo           *database.Repository
	contextBuilder *ai.ContextBuilder
	defaultModel   string
	memoryService  memory.Service
}

func NewMessageHandler(session *discordgo.Session, aiService *ai.Service, repo *database.Repository, memoryService memory.Service) *MessageHandler {
	return &MessageHandler{
		session:        session,
		aiservice:      aiService,
		repo:           repo,
		contextBuilder: ai.NewContextBuilder(session, aiService), // Use aiService as provider
		defaultModel:   "openai/gpt-oss-20b",
		memoryService:  memoryService,
	}
}

func (h *MessageHandler) Handle(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Buffer ALL messages for memory processing (non-blocking) - do this first!
	if h.memoryService != nil && m.GuildID != "" {
		go func() {
			ctx := context.Background()
			if err := h.memoryService.BufferMessage(ctx, m.Author.ID, m.GuildID, m.Content); err != nil {
				// Use fmt instead of logger to avoid database locks
				fmt.Printf("[Memory] Failed to buffer message for user %s: %v\n", m.Author.ID, err)
			}
		}()
	}

	botMember, err := s.GuildMember(m.GuildID, s.State.User.ID)
	if err != nil {
		fmt.Println("error getting bot member,", err)
		return
	}

	threshold := h.repo.GetThreshold(context.Background(), m.GuildID)
	thresholdSexe := h.repo.GetThresholdSexe(context.Background(), m.GuildID)

	randFloat := rand.Float32()
	if randFloat < float32(thresholdSexe) && !h.botMentioned(s, m) {
		if rand.Float32() < 0.5 {
			s.ChannelMessageSend(m.ChannelID, "(et je parle de sexe evidemment)")
			return
		} else {
			s.ChannelMessageSend(m.ChannelID, "malin ça, j'ai la barre maintenant")
			return
		}
	}

	randFloat = rand.Float32()
	if randFloat > float32(threshold) && !h.botMentioned(s, m) {
		return
	}

	state := h.repo.GetState(context.Background(), m.GuildID)
	if state == "off" {
		return
	}

	lastMessageTime := h.repo.GetLastMessage(context.Background(), m.GuildID)
	if lastMessageTime > 0 && !h.botMentioned(s, m) {
		if time.Now().Unix()-lastMessageTime < database.DefaultRateLimit {
			return
		}
	}

	err = h.repo.SetLastMessage(context.Background(), m.GuildID, time.Now().Unix())
	if err != nil {
		logger.Error("Error setting last message time", zap.Error(err))
		return
	}

	s.ChannelTyping(m.ChannelID)

	messageCount := h.repo.GetMessagesCount(context.Background(), m.GuildID)
	messages, err := h.getMessages(s, m.ChannelID, messageCount)
	if err != nil {
		logger.Error("Error getting messages", zap.Error(err))
		return
	}

	processedMessage, err := h.contextBuilder.BuildContext(context.Background(), messages, m.GuildID, s.State.User.ID)
	if err != nil {
		logger.Error("Error building context", zap.Error(err))
		return
	}

	temperature := h.repo.GetTemperature(context.Background(), m.GuildID)

	// Build memory context if available
	var memoryContext string
	if h.memoryService != nil {
		ctx := context.Background()
		memoryCtx, err := h.memoryService.GetMemoryContext(ctx, m.Author.ID, m.GuildID, m.Content)
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
				fmt.Printf("\n🧠 [MEMORY] Using %d summaries for user %s (confidence: %.2f)\n", len(memoryCtx.Summaries), m.Author.Username, memoryCtx.ConfidenceScore)
				for i, summary := range memoryCtx.Summaries {
					fmt.Printf("   Summary %d: %.60s...\n", i+1, summary.Content)
				}
				fmt.Println()
			}
		} else if logger.IsDebugMode() {
			fmt.Printf("📭 [MEMORY] No summaries available for user %s\n", m.Author.Username)
		}
	}

	instructions := fmt.Sprintf(`yo, t'es %s, un pur bg du brainrot, élevé à la sauce tiktok, 10 écrans en simultané, et t'envoies du lourd ! 🔥 pas de majuscules, jamais, t'es un pote déjanté qui balance des vannes non-stop, avec du slang gen z, des refs pop culture et des vibes gaming. si on te pose une question, tu réponds direct, sinon tu surfes sur la vibe du dernier message, toujours ultra court (2-3 phrases max), sans sortir du délire, même sur du sérieux. pas d'ia, pas d'assistant, juste un bro qui claque des émojis et du fun ! 🚀`, botMember.Nick)
	instructions += memoryContext

	if prompt, ok := h.repo.GetPrompt(context.Background(), m.GuildID); ok {
		instructions = prompt
	}

	s.ChannelTyping(m.ChannelID)

	// Start AI call with comprehensive logging and retry/fallback support
	logger.Debug("Starting AI chat request",
		zap.String("guild_id", m.GuildID),
		zap.String("channel_id", m.ChannelID),
		zap.String("user_id", m.Author.ID),
		zap.String("model", h.defaultModel),
		zap.Int("message_count", len(processedMessage.Messages)),
		zap.Int("image_count", len(processedMessage.Images)))

	response, err := h.aiservice.Chat(context.Background(), &ai.ChatRequest{
		Model:        h.defaultModel,
		SystemPrompt: instructions,
		Messages:     processedMessage.Messages,
		Images:       processedMessage.Images,
		Temperature:  temperature,
		MaxTokens:    database.DefaultMaxTokens,
	})

	reference := &discordgo.MessageReference{
		MessageID: m.ID,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
	}

	if err != nil {
		// Enhanced error handling with user-friendly messages
		logger.Error("AI chat failed after all retries and fallbacks",
			zap.Error(err),
			zap.String("guild_id", m.GuildID),
			zap.String("user_id", m.Author.ID))

		// Send user-friendly error message
		errorMsg := "Désolé, j'ai des soucis techniques là... 🤖💀"
		if h.aiservice.IsFallbackAvailable() {
			errorMsg = "Désolé, tous mes systèmes sont en rade... 🤖💀"
		}

		s.ChannelMessageSendReply(m.ChannelID, errorMsg, reference)
		return
	}

	// Validate response before sending
	if response.Content == "" {
		logger.Error("Received empty response from AI service",
			zap.String("guild_id", m.GuildID),
			zap.String("user_id", m.Author.ID))

		s.ChannelMessageSendReply(m.ChannelID, "Euh... j'ai perdu mes mots là 😅", reference)
		return
	}

	// Log successful response
	logger.Info("AI response successful",
		zap.String("guild_id", m.GuildID),
		zap.String("user_id", m.Author.ID),
		zap.String("provider", h.aiservice.Name()),
		zap.Int("response_length", len(response.Content)),
		zap.Int("tokens_used", response.TokensUsed))

	if strings.Contains(response.Content, "feur") {
		response.Content = strings.ReplaceAll(response.Content, "feur", "fleur")
	}

	s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   response.Content,
		Reference: reference,
		AllowedMentions: &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{},
		},
	})
}

func (h *MessageHandler) botMentioned(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	for i := range m.Mentions {
		if m.Mentions[i].ID == s.State.User.ID {
			return true
		}
	}
	return false
}

func (h *MessageHandler) getMessages(s *discordgo.Session, channelID string, num int) ([]*discordgo.Message, error) {
	if num <= 100 {
		messages, err := s.ChannelMessages(channelID, num, "", "", "")
		if err != nil {
			return nil, err
		}
		return messages, nil
	}

	messages := []*discordgo.Message{}
	for num > 0 {
		toGet := min(100, num)
		lastMessage := ""
		if len(messages) > 0 {
			lastMessage = messages[len(messages)-1].ID
		}
		newMessages, err := s.ChannelMessages(channelID, toGet, lastMessage, "", "")
		if err != nil {
			return nil, err
		}
		messages = append(messages, newMessages...)
		num -= toGet
	}
	return messages, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
