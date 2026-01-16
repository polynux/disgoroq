package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	mockSession := &discordgo.Session{}

	registry := NewRegistry(mockSession, true)

	require.NotNil(t, registry)
	assert.Equal(t, mockSession, registry.session)
	assert.True(t, registry.local)
	assert.NotNil(t, registry.commands)
	assert.NotNil(t, registry.handlers)
	assert.Len(t, registry.commands, 0)
	assert.Len(t, registry.handlers, 0)
}

func TestRegistry_AddCommand(t *testing.T) {
	mockSession := &discordgo.Session{}
	registry := NewRegistry(mockSession, false)

	testHandler := func(s *discordgo.Session, i *discordgo.InteractionCreate) {}

	testCommand := &discordgo.ApplicationCommand{
		Name:        "test",
		Description: "Test command",
	}

	registry.AddCommand(testCommand, testHandler)

	assert.Len(t, registry.commands, 1)
	assert.Equal(t, testCommand, registry.commands[0])
	assert.Len(t, registry.handlers, 1)
	handler, ok := registry.handlers["test"]
	assert.True(t, ok)
	assert.NotNil(t, handler)
}

func TestRegistry_AddCommand_Multiple(t *testing.T) {
	mockSession := &discordgo.Session{}
	registry := NewRegistry(mockSession, false)

	handler1 := func(s *discordgo.Session, i *discordgo.InteractionCreate) {}
	handler2 := func(s *discordgo.Session, i *discordgo.InteractionCreate) {}

	cmd1 := &discordgo.ApplicationCommand{Name: "cmd1", Description: "Command 1"}
	cmd2 := &discordgo.ApplicationCommand{Name: "cmd2", Description: "Command 2"}

	registry.AddCommand(cmd1, handler1)
	registry.AddCommand(cmd2, handler2)

	assert.Len(t, registry.commands, 2)
	assert.Len(t, registry.handlers, 2)

	_, ok1 := registry.handlers["cmd1"]
	_, ok2 := registry.handlers["cmd2"]
	assert.True(t, ok1)
	assert.True(t, ok2)
}

func TestRegistry_HandleCommand_Existing(t *testing.T) {
	mockSession := &discordgo.Session{}
	registry := NewRegistry(mockSession, false)

	called := false
	testHandler := func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		called = true
	}

	testCommand := &discordgo.ApplicationCommand{Name: "test", Description: "Test"}
	registry.AddCommand(testCommand, testHandler)

	interactionCreate := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "test",
			},
		},
	}

	registry.HandleCommand(interactionCreate)

	assert.True(t, called, "Handler should have been called")
}

func TestRegistry_HandleCommand_NonExistent(t *testing.T) {
	mockSession := &discordgo.Session{}
	registry := NewRegistry(mockSession, false)

	testHandler := func(s *discordgo.Session, i *discordgo.InteractionCreate) {}
	testCommand := &discordgo.ApplicationCommand{Name: "test", Description: "Test"}
	registry.AddCommand(testCommand, testHandler)

	interactionCreate := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "nonexistent",
			},
		},
	}

	registry.HandleCommand(interactionCreate)

	assert.False(t, false, "Handler should not panic for nonexistent command")
}
