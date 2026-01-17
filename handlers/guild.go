package handlers

import (
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

func HandleGuildCreate(s *discordgo.Session, m *discordgo.GuildCreate) {
	logger.Info("Bot joined guild",
		zap.String("guild_name", m.Guild.Name),
		zap.String("guild_id", m.Guild.ID),
	)
}

func HandleGuildDelete(s *discordgo.Session, m *discordgo.GuildDelete) {
	logger.Info("Bot left guild",
		zap.String("guild_id", m.ID),
	)
}
