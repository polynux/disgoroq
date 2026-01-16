package handlers

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

func HandleGuildCreate(s *discordgo.Session, m *discordgo.GuildCreate) {
	log.Printf("Bot joined guild: %s (%s)", m.Guild.Name, m.Guild.ID)
}

func HandleGuildDelete(s *discordgo.Session, m *discordgo.GuildDelete) {
	log.Printf("Bot left guild: %s", m.ID)
}
