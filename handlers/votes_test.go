package handlers

import (
	"testing"
	"time"

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

func TestIsVotable(t *testing.T) {
	botID := snowflake.ID(1)
	human := discord.Message{Author: discord.User{ID: 2}, Content: "hello"}
	cases := []struct {
		name           string
		message        discord.Message
		want, botReply bool
	}{
		{"human text", human, true, false},
		{"other bot", discord.Message{Author: discord.User{ID: 2, Bot: true}, Content: "hello"}, false, false},
		{"system", discord.Message{Author: discord.User{ID: 2, System: true}, Content: "hello"}, false, false},
		{"webhook", discord.Message{Author: discord.User{ID: 2}, Content: "hello", WebhookID: ptrSnowflake(4)}, false, false},
		{"interaction", discord.Message{Author: discord.User{ID: 2}, Content: "hello", Interaction: &discord.MessageInteraction{}}, false, false},
		{"interaction metadata", discord.Message{Author: discord.User{ID: 2}, Content: "hello", InteractionMetadata: &discord.InteractionMetadata{}}, false, false},
		{"empty", discord.Message{Author: discord.User{ID: 2}}, false, false},
		{"attachment only", discord.Message{Author: discord.User{ID: 2}, Attachments: []discord.Attachment{{}}}, false, false},
		{"AI reply", discord.Message{Author: discord.User{ID: botID}, Content: "answer", MessageReference: &discord.MessageReference{}}, true, true},
		{"bot non-reply", discord.Message{Author: discord.User{ID: botID}, Content: "answer"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, isBotReply := isVotable(tc.message, botID)
			if got != tc.want || isBotReply != tc.botReply {
				t.Fatalf("isVotable() = (%v, %v), want (%v, %v)", got, isBotReply, tc.want, tc.botReply)
			}
		})
	}
}

func TestMessageVoteFromDiscord(t *testing.T) {
	botID, sourceID := snowflake.ID(1), snowflake.ID(42)
	name := "Human Name"
	message := discord.Message{
		ID: 7, ChannelID: 3, Author: discord.User{ID: 9, Username: "user", GlobalName: &name},
		Content: "reply text", CreatedAt: time.Unix(1_700_000_000, 0),
		MessageReference:  &discord.MessageReference{MessageID: &sourceID},
		ReferencedMessage: &discord.Message{ID: sourceID, Content: "original"},
	}
	vote := messageVoteFromDiscord(message, snowflake.ID(8), false)
	if vote.MessageID != "7" || vote.GuildID != "8" || vote.ChannelID != "3" || vote.AuthorID != "9" ||
		vote.AuthorName != "Human Name" || vote.IsBotReply != 0 || vote.Content != "reply text" ||
		vote.MessageCreatedAt != 1_700_000_000 || vote.ReplyToMessageID != "42" || vote.ReplyToContent != "original" {
		t.Fatalf("messageVoteFromDiscord() = %+v", vote)
	}
	if isAIReply(discord.Message{Author: discord.User{ID: botID}, Content: "reply", MessageReference: &discord.MessageReference{}}, botID) == false {
		t.Fatal("valid AI reply rejected")
	}
}

func ptrSnowflake(id snowflake.ID) *snowflake.ID { return &id }
