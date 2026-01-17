package commands

import (
	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

type Registry struct {
	session  *discordgo.Session
	commands []*discordgo.ApplicationCommand
	handlers map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate)
	local    bool
}

func NewRegistry(session *discordgo.Session, local bool) *Registry {
	return &Registry{
		session:  session,
		commands: make([]*discordgo.ApplicationCommand, 0),
		handlers: make(map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate)),
		local:    local,
	}
}

func (r *Registry) AddCommand(cmd *discordgo.ApplicationCommand, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	r.commands = append(r.commands, cmd)
	r.handlers[cmd.Name] = handler
}

func (r *Registry) Register() error {
	_, err := r.session.ApplicationCommandBulkOverwrite(r.session.State.User.ID, "", r.commands)
	if err != nil {
		logger.Log.Error("Error registering commands", zap.Error(err), zap.Int("count", len(r.commands)))
		return err
	}
	logger.Log.Info("Commands registered successfully", zap.Int("count", len(r.commands)))
	return nil
}

func (r *Registry) HandleCommand(i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	handler, ok := r.handlers[data.Name]
	if !ok {
		logger.Log.Warn("Unknown command received",
			zap.String("command", data.Name),
			zap.String("guild_id", i.GuildID),
			zap.String("user_id", i.Member.User.ID),
		)
		return
	}

	logger.Log.Debug("Command received",
		zap.String("command", data.Name),
		zap.String("guild_id", i.GuildID),
		zap.String("user_id", i.Member.User.ID),
	)

	handler(r.session, i)
}
