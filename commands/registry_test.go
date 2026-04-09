package commands

import (
	"testing"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"polynux/disgoroq/config"
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

func TestRegisterAll_PromptCommandSupportsLongInputAndFileUpload(t *testing.T) {
	var client *bot.Client
	registry, err := NewRegistry(client, false, nil)
	require.NoError(t, err)

	RegisterAll(registry, nil, nil, config.ReengageConfig{}, "default prompt", "", nil, client)

	command := findSlashCommand(t, registry.commands, "prompt")
	appendOption := findSubCommandOption(t, command.Options, "append")
	appendTextOption := appendOption.Options[0].(discord.ApplicationCommandOptionString)
	require.NotNil(t, appendTextOption.MaxLength)
	assert.Equal(t, promptInlineMaxLength, *appendTextOption.MaxLength)

	setGroup := findSubCommandGroupOption(t, command.Options, "set")
	require.Len(t, setGroup.Options, 3)

	customOption := setGroup.Options[0]
	require.Equal(t, "custom", customOption.Name)
	customPromptOption := customOption.Options[0].(discord.ApplicationCommandOptionString)
	require.NotNil(t, customPromptOption.MaxLength)
	assert.Equal(t, promptInlineMaxLength, *customPromptOption.MaxLength)

	fileOption := setGroup.Options[1]
	require.Equal(t, "file", fileOption.Name)
	_, ok := fileOption.Options[0].(discord.ApplicationCommandOptionAttachment)
	assert.True(t, ok)
}

func TestRegisterVoiceCommands_PromptCommandSupportsLongInputAndFileUpload(t *testing.T) {
	var client *bot.Client
	registry, err := NewRegistry(client, false, nil)
	require.NoError(t, err)

	RegisterVoiceCommands(registry, NewVoiceCommands(nil, nil, client, "voice prompt"))

	command := findSlashCommand(t, registry.commands, "voice")
	promptGroup := findSubCommandGroupOption(t, command.Options, "prompt")
	require.Len(t, promptGroup.Options, 5)

	appendOption := promptGroup.Options[1]
	require.Equal(t, "append", appendOption.Name)
	appendTextOption := appendOption.Options[0].(discord.ApplicationCommandOptionString)
	require.NotNil(t, appendTextOption.MaxLength)
	assert.Equal(t, promptInlineMaxLength, *appendTextOption.MaxLength)

	setOption := promptGroup.Options[2]
	require.Equal(t, "set", setOption.Name)
	setPromptOption := setOption.Options[0].(discord.ApplicationCommandOptionString)
	require.NotNil(t, setPromptOption.MaxLength)
	assert.Equal(t, promptInlineMaxLength, *setPromptOption.MaxLength)

	fileOption := promptGroup.Options[3]
	require.Equal(t, "file", fileOption.Name)
	_, ok := fileOption.Options[0].(discord.ApplicationCommandOptionAttachment)
	assert.True(t, ok)
}

func findSlashCommand(t *testing.T, commands []discord.ApplicationCommandCreate, name string) discord.SlashCommandCreate {
	t.Helper()

	for _, command := range commands {
		slashCommand, ok := command.(discord.SlashCommandCreate)
		if ok && slashCommand.Name == name {
			return slashCommand
		}
	}

	t.Fatalf("command %q not found", name)
	return discord.SlashCommandCreate{}
}

func findSubCommandOption(t *testing.T, options []discord.ApplicationCommandOption, name string) discord.ApplicationCommandOptionSubCommand {
	t.Helper()

	for _, option := range options {
		subCommand, ok := option.(discord.ApplicationCommandOptionSubCommand)
		if ok && subCommand.Name == name {
			return subCommand
		}
	}

	t.Fatalf("subcommand %q not found", name)
	return discord.ApplicationCommandOptionSubCommand{}
}

func findSubCommandGroupOption(t *testing.T, options []discord.ApplicationCommandOption, name string) discord.ApplicationCommandOptionSubCommandGroup {
	t.Helper()

	for _, option := range options {
		subCommandGroup, ok := option.(discord.ApplicationCommandOptionSubCommandGroup)
		if ok && subCommandGroup.Name == name {
			return subCommandGroup
		}
	}

	t.Fatalf("subcommand group %q not found", name)
	return discord.ApplicationCommandOptionSubCommandGroup{}
}
