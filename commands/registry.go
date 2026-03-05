package commands

import (
	stdcontext "context"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

type Registry struct {
	client   *bot.Client
	commands []discord.ApplicationCommandCreate
	handlers map[string]func(e *events.ApplicationCommandInteractionCreate)
	local    bool
}

func NewRegistry(client *bot.Client, local bool) *Registry {
	return &Registry{
		client:   client,
		commands: make([]discord.ApplicationCommandCreate, 0),
		handlers: make(map[string]func(e *events.ApplicationCommandInteractionCreate)),
		local:    local,
	}
}

func (r *Registry) AddCommand(cmd discord.ApplicationCommandCreate, handler func(e *events.ApplicationCommandInteractionCreate)) {
	r.commands = append(r.commands, cmd)
	name := ""
	switch c := cmd.(type) {
	case discord.SlashCommandCreate:
		name = c.Name
	case discord.UserCommandCreate:
		name = c.Name
	case discord.MessageCommandCreate:
		name = c.Name
	}
	r.handlers[name] = handler
}

func (r *Registry) Register(ctx stdcontext.Context) error {
	commands, err := r.client.Rest.SetGlobalCommands(r.client.ID(), r.commands, rest.WithCtx(ctx))
	if err != nil {
		logger.Error("Error registering commands", zap.Error(err), zap.Int("count", len(r.commands)))
		return err
	}
	logger.Info("Commands registered successfully", zap.Int("count", len(commands)))
	return nil
}

func (r *Registry) HandleCommand(e *events.ApplicationCommandInteractionCreate) {
	data := e.SlashCommandInteractionData()

	handler, ok := r.handlers[data.CommandName()]
	if !ok {
		userID := ""
		if e.Member() != nil {
			userID = e.Member().User.ID.String()
		}

		guildIDStr := ""
		if e.GuildID() != nil {
			guildIDStr = e.GuildID().String()
		}

		logger.Warn("Unknown command received",
			zap.String("command", data.CommandName()),
			zap.String("guild_id", guildIDStr),
			zap.String("user_id", userID),
		)
		return
	}

	userID := ""
	if e.Member() != nil {
		userID = e.Member().User.ID.String()
	}

	guildIDStr := ""
	if e.GuildID() != nil {
		guildIDStr = e.GuildID().String()
	}

	logger.Debug("Command received",
		zap.String("command", data.CommandName()),
		zap.String("guild_id", guildIDStr),
		zap.String("user_id", userID),
	)

	handler(e)
}
