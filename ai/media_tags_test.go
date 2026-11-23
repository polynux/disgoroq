package ai

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type mediaTagsFixture struct {
	Contract string `json:"contract"`
	Cases    []struct {
		Name        string `json:"name"`
		Content     string `json:"content"`
		Attachments []struct {
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
		} `json:"attachments"`
		Stickers []string `json:"stickers"`
		Embeds   []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"embeds"`
		V0 string `json:"v0"`
		V1 string `json:"v1"`
	} `json:"cases"`
}

func TestRenderMediaMessageIgnoresQueryWhenExtractingPath(t *testing.T) {
	msg := Message{Content: "https://cdn.discordapp.com?next=/cat-gif"}
	require.Equal(t, "[image]", RenderMediaMessage(msg, MediaTagsV1))
	require.Equal(t, "[image]", RenderMediaMessage(msg, MediaTagsV0))
}

func TestRenderMediaMessageSchemelessURLWithURLQuery(t *testing.T) {
	msg := Message{Content: "www.cdn.discordapp.com/attachments/1/2/photo.png?next=https://example.com/cat-gif"}
	require.Equal(t, "[image]", RenderMediaMessage(msg, MediaTagsV1))
	require.Equal(t, "[image]", RenderMediaMessage(msg, MediaTagsV0))
}

func TestRenderMediaMessageAttachmentTypeRules(t *testing.T) {
	cases := []struct {
		name        string
		filename    string
		contentType string
		want        string
	}{
		{name: "no dot is not an extension", filename: "mp4", want: "[fichier]"},
		{name: "no dot png", filename: "png", want: "[fichier]"},
		{name: "bare image type", filename: "photo.png", contentType: "image", want: "[fichier]"},
		{name: "prefixed image type", filename: "photo.png", contentType: "imagex/y", want: "[fichier]"},
		{name: "explicit image type", filename: "photo.png", contentType: "image/png", want: "[image]"},
		{name: "inferred image type", filename: "photo.PNG", want: "[image]"},
		{name: "inferred video type", filename: "clip.mp4", want: "[video]"},
		{name: "inferred audio type", filename: "sound.mp3", want: "[son]"},
		{name: "content type parameters", filename: "clip.bin", contentType: "video/mp4; codecs=avc1", want: "[video]"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			msg := Message{Attachments: []MessageAttachment{{Filename: testCase.filename, ContentType: testCase.contentType}}}
			require.Equal(t, testCase.want, RenderMediaMessage(msg, MediaTagsV1))
		})
	}
}

func TestRenderMediaMessageStopsURLAtUnicodeSpace(t *testing.T) {
	for _, space := range []string{"\u00a0", "\u202f"} {
		msg := Message{Content: "https://tenor.com/view/cat-gif" + space + "mdr"}
		require.Equal(t, "[gif: cat] mdr", RenderMediaMessage(msg, MediaTagsV1))
		require.Equal(t, "[gif] mdr", RenderMediaMessage(msg, MediaTagsV0))
	}
}

func TestGifWordsUseASCIIDigitsAndCodePoints(t *testing.T) {
	require.Equal(t, []string{"١٢٣"}, gifWordsFromSegment("١٢٣-gif"))
	require.Equal(t, []string{"éééé1"}, gifWordsFromSegment("éééé1-gif"))
	require.Empty(t, gifWordsFromSegment("aaaaaaaa1-gif"))
}

func TestUnquoteMediaSegmentMatchesPython(t *testing.T) {
	require.Equal(t, "\uFFFD", unquoteMediaSegment("%E2%82"))
	require.Equal(t, "\uFFFD\uFFFD", unquoteMediaSegment("%FF%FF"))
	require.Equal(t, "\uFFFD(\uFFFD", unquoteMediaSegment("%E2%28%A1"))
	require.Equal(t, "€", unquoteMediaSegment("%E2%82%AC"))
	require.Equal(t, "100%", unquoteMediaSegment("100%"))
}

func TestRenderMediaMessageFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/media_tags_v1.json")
	require.NoError(t, err)

	var fixture mediaTagsFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.NotEmpty(t, fixture.Cases)

	for _, testCase := range fixture.Cases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			message := Message{Content: testCase.Content}
			for _, attachment := range testCase.Attachments {
				message.Attachments = append(message.Attachments, MessageAttachment{
					Filename:    attachment.Filename,
					ContentType: attachment.ContentType,
				})
			}
			message.Stickers = testCase.Stickers
			for _, embed := range testCase.Embeds {
				message.Embeds = append(message.Embeds, MessageEmbed{
					URL:         embed.URL,
					Title:       embed.Title,
					Description: embed.Description,
				})
			}

			require.Equal(t, testCase.V0, RenderMediaMessage(message, MediaTagsV0), "v0")
			require.Equal(t, testCase.V1, RenderMediaMessage(message, MediaTagsV1), "v1")
		})
	}
}

func TestParseMediaTags(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  []MediaTag
	}{
		{
			name:  "bare gif",
			reply: "mdr [gif]",
			want:  []MediaTag{{Raw: "[gif]", Kind: "gif", Description: ""}},
		},
		{
			name:  "described gif with extra spaces and uppercase",
			reply: "regardez [GIF:   cat   smile  ]",
			want:  []MediaTag{{Raw: "[GIF:   cat   smile  ]", Kind: "gif", Description: "cat smile"}},
		},
		{
			name:  "gif with empty colon treated as bare gif",
			reply: "[gif:   ]",
			want:  []MediaTag{{Raw: "[gif:   ]", Kind: "gif", Description: ""}},
		},
		{
			name:  "described link kinds preserve inner colons",
			reply: "tiens [youtube: Rick: Astley] et [tweet: breaking: news] [tiktok: danse] [insta: photo] [son: track] [steam: Half-Life 2]",
			want: []MediaTag{
				{Raw: "[youtube: Rick: Astley]", Kind: "youtube", Description: "Rick: Astley"},
				{Raw: "[tweet: breaking: news]", Kind: "tweet", Description: "breaking: news"},
				{Raw: "[tiktok: danse]", Kind: "tiktok", Description: "danse"},
				{Raw: "[insta: photo]", Kind: "insta", Description: "photo"},
				{Raw: "[son: track]", Kind: "son", Description: "track"},
				{Raw: "[steam: Half-Life 2]", Kind: "steam", Description: "Half-Life 2"},
			},
		},
		{
			name:  "bare link tags and non-resolvable tags are ignored",
			reply: "salut [youtube] [tweet:] [tiktok:  ] [image] [lien] [lien: site] [video] [fichier] [sticker: Wave]",
			want:  nil,
		},
		{
			name:  "plain text has no tags",
			reply: "juste du texte normal",
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ParseMediaTags(tc.reply))
		})
	}
}

func TestDiscordCDNAndCanonicalize(t *testing.T) {
	require.True(t, IsDiscordCDNURL("https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2"))
	require.True(t, IsDiscordCDNURL("https://MEDIA.DISCORDAPP.NET/attachments/1/2/cat.gif?width=100"))
	require.False(t, IsDiscordCDNURL("https://tenor.com/view/cat-gif-123"))
	require.False(t, IsDiscordCDNURL("https://example.com/?ref=cdn.discordapp.com"))

	require.Equal(t,
		"https://cdn.discordapp.com/attachments/1/2/cat.gif",
		CanonicalizeMediaURL("https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2"),
	)
	require.Equal(t,
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		CanonicalizeMediaURL("https://www.youtube.com/watch?v=dQw4w9WgXcQ"),
	)
}

func TestExtractMediaURLs(t *testing.T) {
	msg := Message{
		Content: "regarde https://tenor.com/view/cat-smile-gif-12345678, " +
			"https://cdn.discordapp.com/attachments/1/2/funny-cat.gif?ex=abc&is=def! " +
			"https://youtu.be/abc123. " +
			"https://twitter.com/user/status/42 " +
			"https://example.com/page " +
			"https://tenor.com/view/12345678",
		Embeds: []MessageEmbed{
			{URL: "https://youtu.be/abc123", Title: "Best Video [HD]"},
			{URL: "https://twitter.com/user/status/42", Description: "Tweet body"},
		},
	}

	got := ExtractMediaURLs(msg)
	require.Equal(t, []ExtractedMedia{
		{URL: "https://tenor.com/view/cat-smile-gif-12345678", Kind: "gif", Description: "cat smile"},
		{URL: "https://cdn.discordapp.com/attachments/1/2/funny-cat.gif", Kind: "gif", Description: "funny cat"},
		{URL: "https://youtu.be/abc123", Kind: "youtube", Description: "Best Video HD"},
		{URL: "https://twitter.com/user/status/42", Kind: "tweet", Description: "Tweet body"},
		{URL: "https://tenor.com/view/12345678", Kind: "gif", Description: ""},
	}, got)

	// Undescribed link without embed is not extracted.
	undescribed := Message{Content: "https://youtu.be/noembed"}
	require.Empty(t, ExtractMediaURLs(undescribed))

	// Duplicate Discord CDN URL with different query tokens is deduplicated.
	dupCDN := Message{
		Content: "https://cdn.discordapp.com/attachments/1/2/dance.gif?ex=1 https://cdn.discordapp.com/attachments/1/2/dance.gif?ex=2",
	}
	require.Equal(t, []ExtractedMedia{
		{URL: "https://cdn.discordapp.com/attachments/1/2/dance.gif", Kind: "gif", Description: "dance"},
	}, ExtractMediaURLs(dupCDN))
}

func TestAssembleMediaReply(t *testing.T) {
	require.Equal(t, "mdr\nhttps://tenor.com/view/cat-1", AssembleMediaReply("mdr", "https://tenor.com/view/cat-1"))
	require.Equal(t, "https://tenor.com/view/cat-1", AssembleMediaReply("", "https://tenor.com/view/cat-1"))
	require.Equal(t, "https://tenor.com/view/cat-1", AssembleMediaReply("   ", "https://tenor.com/view/cat-1"))
	require.Equal(t, "mdr", AssembleMediaReply("mdr", ""))
	require.Equal(t, "", AssembleMediaReply("", ""))
}

