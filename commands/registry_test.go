package commands

import (
	"testing"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	// Note: creating a real bot.Client requires a token, so we test the structure
	// In real tests, we would mock the client
	var client *bot.Client

	registry, err := NewRegistry(client, true, []string{"123456789012345678"})
	require.NoError(t, err)

	require.NotNil(t, registry)
	assert.True(t, registry.local)
	assert.NotNil(t, registry.commands)
	assert.NotNil(t, registry.handlers)
	assert.Len(t, registry.devGuilds, 1)
	assert.Len(t, registry.commands, 0)
	assert.Len(t, registry.handlers, 0)
}

func TestNewRegistry_ProductionModeDoesNotRequireDevGuilds(t *testing.T) {
	var client *bot.Client

	registry, err := NewRegistry(client, false, nil)
	require.NoError(t, err)
	assert.False(t, registry.local)
	assert.Empty(t, registry.devGuilds)
}

func TestNewRegistry_InvalidDevGuildID(t *testing.T) {
	var client *bot.Client

	registry, err := NewRegistry(client, true, []string{"not-a-snowflake"})
	require.Error(t, err)
	assert.Nil(t, registry)
}

func TestRegistry_AddCommand(t *testing.T) {
	var client *bot.Client
	registry, err := NewRegistry(client, false, nil)
	require.NoError(t, err)

	testHandler := func(e *events.ApplicationCommandInteractionCreate) {}

	testCommand := discord.SlashCommandCreate{
		Name:        "test",
		Description: "Test command",
	}

	registry.AddCommand(testCommand, testHandler)

	assert.Len(t, registry.commands, 1)
	registered, ok := registry.commands[0].(discord.SlashCommandCreate)
	require.True(t, ok)
	assert.Equal(t, "test", registered.Name)
	assert.Len(t, registry.handlers, 1)
	handler, ok := registry.handlers["test"]
	assert.True(t, ok)
	assert.NotNil(t, handler)
}

func TestRegistry_AddCommand_Multiple(t *testing.T) {
	var client *bot.Client
	registry, err := NewRegistry(client, false, nil)
	require.NoError(t, err)

	handler1 := func(e *events.ApplicationCommandInteractionCreate) {}
	handler2 := func(e *events.ApplicationCommandInteractionCreate) {}

	cmd1 := discord.SlashCommandCreate{Name: "cmd1", Description: "Command 1"}
	cmd2 := discord.SlashCommandCreate{Name: "cmd2", Description: "Command 2"}

	registry.AddCommand(cmd1, handler1)
	registry.AddCommand(cmd2, handler2)

	assert.Len(t, registry.commands, 2)
	assert.Len(t, registry.handlers, 2)

	_, ok1 := registry.handlers["cmd1"]
	_, ok2 := registry.handlers["cmd2"]
	assert.True(t, ok1)
	assert.True(t, ok2)
}

// Note: HandleCommand would require mocking bot.Client and events
// These would be integration tests that require a mock client implementation
