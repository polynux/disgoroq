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
