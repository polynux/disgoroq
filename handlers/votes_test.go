package handlers

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
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

func TestIsAIReplyAndAdoptedReplyVote(t *testing.T) {
	botID, sourceID := snowflake.ID(1), snowflake.ID(42)
	reply := discord.Message{
		ID: 7, ChannelID: 3, Author: discord.User{ID: botID}, Content: "mdr ouais",
		MessageReference:  &discord.MessageReference{MessageID: &sourceID},
		ReferencedMessage: &discord.Message{ID: sourceID, Content: "t'es d'accord ?"},
	}
	if !isAIReply(reply, botID) {
		t.Fatal("isAIReply(bot reply) = false, want true")
	}

	notReply := reply
	notReply.MessageReference = nil
	otherAuthor := reply
	otherAuthor.Author.ID = 2
	errorReply := reply
	errorReply.Content = "Désolé, j'ai des soucis techniques là... 🤖💀"
	command := reply
	command.InteractionMetadata = &discord.InteractionMetadata{}
	for name, message := range map[string]discord.Message{
		"no reference": notReply, "other author": otherAuthor, "error reply": errorReply, "command output": command,
	} {
		if isAIReply(message, botID) {
			t.Errorf("isAIReply(%s) = true, want false", name)
		}
	}

	vote := adoptedReplyVote(reply, snowflake.ID(9))
	if vote.MessageID != "7" || vote.GuildID != "9" || vote.ChannelID != "3" || vote.Content != "mdr ouais" ||
		vote.ReplyToMessageID != "42" || vote.ReplyToContent != "t'es d'accord ?" || vote.Provider != "" {
		t.Fatalf("adoptedReplyVote() = %+v", vote)
	}
}
