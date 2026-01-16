package commands

import (
	"log"

	"github.com/bwmarrin/discordgo"
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
		return err
	}
	log.Printf("Registered %d commands", len(r.commands))
	return nil
}

func (r *Registry) HandleCommand(i *discordgo.InteractionCreate) {
	handler, ok := r.handlers[i.ApplicationCommandData().Name]
	if !ok {
		return
	}
	handler(r.session, i)
}
