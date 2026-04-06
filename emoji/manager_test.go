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
