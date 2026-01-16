package handlers

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/bwmarrin/discordgo"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/database"
)

type MessageHandler struct {
	session        *discordgo.Session
	provider       ai.Provider
	repo           *database.Repository
	contextBuilder *ai.ContextBuilder
	defaultModel   string
}

func NewMessageHandler(session *discordgo.Session, provider ai.Provider, repo *database.Repository) *MessageHandler {
	return &MessageHandler{
		session:        session,
		provider:       provider,
		repo:           repo,
		contextBuilder: ai.NewContextBuilder(session, provider),
		defaultModel:   "openai/gpt-oss-20b",
	}
}

func (h *MessageHandler) Handle(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
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
			s.ChannelMessageSend(m.ChannelID, "Please wait a bit before asking me again.")
			return
		}
	}

	err = h.repo.SetLastMessage(context.Background(), m.GuildID, time.Now().Unix())
	if err != nil {
		fmt.Println("error setting last message time,", err)
		return
	}

	s.ChannelTyping(m.ChannelID)

	messageCount := h.repo.GetMessagesCount(context.Background(), m.GuildID)
	messages, err := h.getMessages(s, m.ChannelID, messageCount)
	if err != nil {
		fmt.Println("error getting messages,", err)
		return
	}

	processedMessage, err := h.contextBuilder.BuildContext(context.Background(), messages, m.GuildID, s.State.User.ID)
	if err != nil {
		fmt.Println("error building context,", err)
		return
	}

	temperature := h.repo.GetTemperature(context.Background(), m.GuildID)

	instructions := fmt.Sprintf(`yo, t'es %s, un pur bg du brainrot, élevé à la sauce tiktok, 10 écrans en simultané, et t'envoies du lourd ! 🔥 pas de majuscules, jamais, t'es un pote déjanté qui balance des vannes non-stop, avec du slang gen z, des refs pop culture et des vibes gaming. si on te pose une question, tu réponds direct, sinon tu surfes sur la vibe du dernier message, toujours ultra court (2-3 phrases max), sans sortir du délire, même sur du sérieux. pas d'ia, pas d'assistant, juste un bro qui claque des émojis et du fun ! 🚀`, botMember.Nick)

	if prompt, ok := h.repo.GetPrompt(context.Background(), m.GuildID); ok {
		instructions = prompt
	}

	s.ChannelTyping(m.ChannelID)

	response, err := h.provider.Chat(context.Background(), &ai.ChatRequest{
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
		if h.botMentioned(s, m) {
			s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Content:   "There was an error getting the response.",
				Reference: reference,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse: []discordgo.AllowedMentionType{},
				},
			})
		} else {
			s.ChannelMessageSend(m.ChannelID, "There was an error getting the response.")
		}
		return
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
