package handlers

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
)

func TestReplyVoteCounts(t *testing.T) {
	reactions := []discord.MessageReaction{
		{Count: 4, Me: true, Emoji: discord.Emoji{Name: "😂"}},
		{Count: 2, Me: false, Emoji: discord.Emoji{Name: "🗑"}},
		{Count: 9, Emoji: discord.Emoji{Name: "❤️"}},
	}
	good, bad := ReplyVoteCounts(reactions, "😂", "🗑️")
	if good != 3 || bad != 2 {
		t.Fatalf("ReplyVoteCounts() = (%d, %d), want (3, 2)", good, bad)
	}

	good, bad = ReplyVoteCounts(nil, "😂", "🗑️")
	if good != 0 || bad != 0 {
		t.Fatalf("ReplyVoteCounts(nil) = (%d, %d), want (0, 0)", good, bad)
	}
}
