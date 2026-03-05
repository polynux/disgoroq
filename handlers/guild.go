package handlers

import (
	"github.com/disgoorg/disgo/events"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

func HandleGuildJoin(e *events.GuildJoin) {
	logger.Info("Bot joined guild",
		zap.String("guild_name", e.Guild.Name),
		zap.String("guild_id", e.Guild.ID.String()),
	)
}

func HandleGuildLeave(e *events.GuildLeave) {
	logger.Info("Bot left guild",
		zap.String("guild_id", e.GuildID.String()),
	)
}
