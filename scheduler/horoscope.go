package scheduler

import (
	"context"
	"fmt"

	"github.com/conneroisu/groq-go"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/horoscope"
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

	response, err := s.provider.Chat(context.Background(), &ai.ChatRequest{
		Model:        string(groq.ModelLlama3370BVersatile),
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
		fmt.Println("error getting response,", err)
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
		fmt.Println("error getting guilds,", err)
		return
	}

	for _, guild := range guilds {
		channelID, err := s.repo.GetHoroscopeChannel(context.Background(), guild)
		if err != nil {
			fmt.Println("error getting horoscope channel,", err)
			continue
		}
		_, err = s.session.ChannelMessageSend(channelID, "Horoscope du jour:")
		if err != nil {
			fmt.Println("error sending horoscope,", err)
			continue
		}
		for _, value := range responses {
			_, err = s.session.ChannelMessageSend(channelID, value)
			if err != nil {
				fmt.Println("error sending horoscope,", err)
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
