package scheduler

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/horoscope"
	"polynux/disgoroq/logger"
)

func (s *Scheduler) SendHoroscope() {
	horoscopes, _ := horoscope.GetHoroscopes()
	horoscopeMessage := ""
	horoscopes.Range(func(key, value any) bool {
		horoscopeMessage += fmt.Sprintf("%s\n%s\n\n", key, value)
		return true
	})

	instructions := `Tu es un créateur d'horoscope DÉLIRANT 🤪. Pour chaque horoscope que tu recevras:

1. Transforme-le en version ULTRA GOOFY avec des prédictions absurdes et exagérées 🥴
2. Limite ta réponse à 2-3 phrases MAXIMUM par thème
3. Saupoudre GÉNÉREUSEMENT d'émojis loufoques (🤪, 👽, 🧠, 🌮, etc.)
4. Utilise un langage décalé et des métaphores ridicules
5. Inclus toujours le signe astrologique en gras au début: "**SIGNE**"
6. Termine par une "recommandation cosmique" totalement farfelue
7. Évite tout conseil sérieux - plus c'est absurde, mieux c'est!

Exemple: "**TAUREAU** Cette semaine, tes plantes d'intérieur complotent pour voler tes chaussettes! 🧦👽 Méfie-toi des carottes qui te font des clins d'œil au supermarché. 🥕👀 Recommandation cosmique: porte ton chapeau à l'envers pour augmenter ton magnétisme auprès des distributeurs automatiques! 🤪💰"`

	// Check if emoji inclusion is enabled from config
	includeEmojis := s.horoscopeCfg.IncludeEmojis

	// Append custom emojis to system prompt if enabled
	if includeEmojis && s.emojiManager != nil {
		allEmojis := s.emojiManager.GetAllEmojis()
		// Limit to 50 emojis to avoid prompt bloat
		if len(allEmojis) > 50 {
			allEmojis = allEmojis[:50]
		}
		if len(allEmojis) > 0 {
			emojiList := s.emojiManager.FormatEmojiList(allEmojis)
			instructions = instructions + "\n\nTu peux aussi utiliser ces emojis personnalisés: " + emojiList
		}
	}

	response, err := s.aiservice.Chat(context.Background(), &ai.ChatRequest{
		SystemPrompt: instructions,
		Messages: []ai.Message{
			{
				Role:    "user",
				Content: horoscopeMessage,
			},
		},
		Temperature: 1,
		MaxTokens:   3000,
	})

	if err != nil {
		logger.Error("Error getting horoscope response", zap.Error(err))

		// Enhanced error handling with fallback awareness
		errorMsg := "Failed to generate horoscope"
		if s.aiservice.IsFallbackAvailable() {
			errorMsg = "Failed to generate horoscope (both primary and fallback providers failed)"
		}
		logger.Error(errorMsg, zap.Error(err))
		return
	}

	responses := make([]string, 0)
	if len(response.Content) > 2000 {
		for i := 0; i < len(response.Content); i += 2000 {
			responses = append(responses, response.Content[i:min(i+2000, len(response.Content))])
		}
	} else {
		responses = append(responses, response.Content)
	}

	guilds, err := s.repo.GetAllGuilds(context.Background())
	if err != nil {
		logger.Error("Error getting guilds", zap.Error(err))
		return
	}

	for _, guild := range guilds {
		channelID, err := s.repo.GetHoroscopeChannel(context.Background(), guild)
		if err != nil {
			logger.Error("Error getting horoscope channel",
				zap.Error(err),
				zap.String("guild_id", guild),
			)
			continue
		}
		_, err = s.session.ChannelMessageSend(channelID, "Horoscope du jour:")
		if err != nil {
			logger.Error("Error sending horoscope header",
				zap.Error(err),
				zap.String("guild_id", guild),
				zap.String("channel_id", channelID),
			)
			continue
		}
		for _, value := range responses {
			_, err = s.session.ChannelMessageSend(channelID, value)
			if err != nil {
				logger.Error("Error sending horoscope content",
					zap.Error(err),
					zap.String("guild_id", guild),
					zap.String("channel_id", channelID),
				)
				continue
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}