package emoji

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeDiscordEmojiShortcodes(t *testing.T) {
	input := "salut <:criminel:1238422591547637800> puis <a:dance:987654321> fin"

	normalized := NormalizeDiscordEmojiShortcodes(input)

	assert.Equal(t, "salut :criminel: puis :dance: fin", normalized)
}

func TestNormalizeDiscordEmojiShortcodes_LeavesPlainTextUnchanged(t *testing.T) {
	input := "salut :wave: et un emoji unicode 😀"

	assert.Equal(t, input, NormalizeDiscordEmojiShortcodes(input))
}

func TestGuildEmojiPrompt_UsesOnlyCurrentGuild(t *testing.T) {
	manager := &Manager{
		cache: map[string]*cacheEntry{
			"guild-1": {
				emojis: []Emoji{
					{Name: "wave", ID: "1", GuildID: "guild-1"},
					{Name: "party", ID: "2", GuildID: "guild-1"},
				},
			},
			"guild-2": {
				emojis: []Emoji{
					{Name: "alien", ID: "3", GuildID: "guild-2"},
				},
			},
		},
	}

	prompt := manager.GuildEmojiPrompt("guild-1", 50)

	assert.Contains(t, prompt, ":wave:")
	assert.Contains(t, prompt, ":party:")
	assert.NotContains(t, prompt, ":alien:")
}

func TestGuildEmojiPrompt_RespectsLimit(t *testing.T) {
	manager := &Manager{
		cache: map[string]*cacheEntry{
			"guild-1": {
				emojis: []Emoji{
					{Name: "one", ID: "1", GuildID: "guild-1"},
					{Name: "two", ID: "2", GuildID: "guild-1"},
				},
			},
		},
	}

	prompt := manager.GuildEmojiPrompt("guild-1", 1)

	assert.Contains(t, prompt, ":one:")
	assert.NotContains(t, prompt, ":two:")
}
