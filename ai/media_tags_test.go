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
