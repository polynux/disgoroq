package scheduler

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

func (s *Scheduler) SendFartingFriday() {
	embed := &discordgo.MessageEmbed{
		Title:       "🎉 FARTING FRIDAY NOTIFICATION 🎉",
		Description: "@everyone Heeeeeeyyyyyy les amis du bruit de fond !!! 💨💨💨",
		Color:       0x8B4513,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🌈✨ JOYEUX FARTING FRIDAY À TOUS LES PÉTOMANES EN HERBE ✨🌈",
				Value:  "Que vos flatulences soient mélodieuses et vos pets harmonieux en ce jour béni où nous célébrons l'art ancestral du prout ! 🎵💨",
				Inline: false,
			},
			{
				Name:   "Rappel Important",
				Value:  "N'oubliez pas : aujourd'hui, c'est pas juste permis, c'est ENCOURAGÉ de lâcher la pression atmosphérique !! 🌪️🌬️",
				Inline: false,
			},
			{
				Name:   "Conseil du jour 💡",
				Value:  "Mangez des haricots pour un boost de performance ! 🫘💪",
				Inline: true,
			},
			{
				Name:   "Astuce pro 🧠",
				Value:  "\"Qui prout dans l'eau fait des bulles, qui prout dans le vent fait du parfum\"",
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "*pffffrrrrrttttt* 💨 (c'était ma signature olfactive)",
		},
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: "https://media.discordapp.net/attachments/1194331990506356780/1362866990980796576/fartfireani.gif?ex=6803f44b&is=6802a2cb&hm=ecbbe318aa782a48b1ee9d1454b10870009bde37e96dcc470337a52d965271af&=",
		},
		Image: &discordgo.MessageEmbedImage{
			URL: "https://media.discordapp.net/attachments/1194331990506356780/1362860968891384119/fartin.gif?ex=6803eeaf&is=68029d2f&hm=120759b2a6258432922d9e61239288ab3226dc925c86009e0402298c6b0e3df5&=",
		},
	}

	guilds, err := s.repo.GetAllGuilds(context.Background())
	if err != nil {
		logger.Log.Error("Error getting guilds", zap.Error(err))
		return
	}

	for _, guild := range guilds {
		channelID, err := s.repo.GetFartingFridayChannel(context.Background(), guild)
		if err != nil {
			logger.Log.Error("Error getting farting friday channel",
				zap.Error(err),
				zap.String("guild_id", guild),
			)
			continue
		}

		_, err = s.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: "🔊 **FARTING FRIDAY EST ARRIVÉ!** 💨 [Cliquez pour entendre le son légendaire](https://www.myinstants.com/en/instant/wet-fart-11093/)",
			Embed:   embed,
			AllowedMentions: &discordgo.MessageAllowedMentions{
				Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeEveryone},
			},
		})

		if err != nil {
			logger.Log.Error("Error sending farting friday",
				zap.Error(err),
				zap.String("guild_id", guild),
				zap.String("channel_id", channelID),
			)
			continue
		}
	}

	logger.Log.Info("Farting Friday notifications sent", zap.Int("guild_count", len(guilds)))
}
