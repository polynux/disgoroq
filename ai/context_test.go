package ai

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockProviderForTest struct{}

func (m *mockProviderForTest) Name() string {
	return "mock"
}

func (m *mockProviderForTest) AvailableModels() []ModelInfo {
	return []ModelInfo{}
}

func (m *mockProviderForTest) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (m *mockProviderForTest) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	return nil, nil
}

func TestNewContextBuilder(t *testing.T) {
	var client *bot.Client
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(client, mockProvider)

	require.NotNil(t, cb)
	assert.Equal(t, client, cb.client)
	assert.NotNil(t, cb.gifProcessor)
	assert.NotNil(t, cb.docProcessor)
}

func TestGetImagesToProcess_NoImages(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		{
			ID:          snowflake.ID(1),
			Content:     "Hello world",
			Attachments: []discord.Attachment{},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ValidImage(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "Check this image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 1)
	assert.Equal(t, "1", images[0].id)
	assert.True(t, strings.HasPrefix(images[0].url, "data:image/jpeg;base64,"))
	assert.Equal(t, "https://example.com/image.jpg", images[0].sourceURL)
	assert.Equal(t, "image/jpeg", images[0].contentType)
	assert.Equal(t, 800, images[0].width)
	assert.Equal(t, 600, images[0].height)
	assert.Equal(t, int64(1000000), images[0].size)
}

func TestGetImagesToProcess_NonImageAttachment(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's a file", []discord.Attachment{
			newAttachment(1, "https://example.com/file.pdf", "application/pdf", 0, 0, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_UnsupportedImageType(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's an image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.svg", "image/svg+xml", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ImageTooLarge(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's a large image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 800, 600, 25000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ImageResolutionTooHigh(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's a high res image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 6000, 6000, 5000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_MultipleImages(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "First message", []discord.Attachment{
			newAttachment(1, "https://example.com/image1.jpg", "image/jpeg", 800, 600, 1000000),
			newAttachment(2, "https://example.com/image2.png", "image/png", 1024, 768, 1500000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 2)
	assert.True(t, strings.HasPrefix(images[0].url, "data:image/jpeg;base64,"))
	assert.True(t, strings.HasPrefix(images[1].url, "data:image/png;base64,"))
}

func TestGetImagesToProcess_LimitToSixImages(t *testing.T) {
	cb := newTestContextBuilder()

	attachments := make([]discord.Attachment, 0, 7)
	for i := 1; i <= 7; i++ {
		attachments = append(attachments, newAttachment(
			snowflake.ID(i),
			"https://example.com/image"+string(rune('0'+i))+".jpg",
			"image/jpeg",
			800,
			600,
			1000000,
		))
	}

	messages := []discord.Message{
		newMessageWithAttachments(1, "Many images", attachments),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 6)
}

func TestGetImagesToProcess_ReversedOrder(t *testing.T) {
	cb := newTestContextBuilder()

	messages := []discord.Message{
		newMessageWithAttachments(1, "First", []discord.Attachment{
			newAttachment(1, "https://example.com/first.jpg", "image/jpeg", 800, 600, 1000000),
		}),
		newMessageWithAttachments(2, "Second", []discord.Attachment{
			newAttachment(2, "https://example.com/second.jpg", "image/jpeg", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 2)
	assert.Equal(t, "2", images[0].id, "Should process images in reverse order")
	assert.Equal(t, "1", images[1].id, "Should process images in reverse order")
}

func TestGetImagesToProcess_SkipsFailedMaterialization(t *testing.T) {
	cb := newTestContextBuilder()
	cb.download = func(ctx context.Context, rawURL string, maxBytes int64) (*remoteContent, error) {
		return nil, assert.AnError
	}

	messages := []discord.Message{
		newMessageWithAttachments(1, "Check this image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_SingleFrameGIFFallsBackToStaticDataURI(t *testing.T) {
	cb := newTestContextBuilder()
	cb.download = func(ctx context.Context, rawURL string, maxBytes int64) (*remoteContent, error) {
		return &remoteContent{
			SourceURL:   rawURL,
			FinalURL:    rawURL,
			ContentType: "image/gif",
			Data:        singleFrameGIFData(t),
		}, nil
	}

	messages := []discord.Message{
		newMessageWithAttachments(1, "Check this gif", []discord.Attachment{
			newAttachment(1, "https://example.com/image.gif", "image/gif", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 1)
	assert.True(t, strings.HasPrefix(images[0].url, "data:image/gif;base64,"))
	assert.Equal(t, "image/gif", images[0].contentType)
	assert.Equal(t, "https://example.com/image.gif", images[0].sourceURL)
}

func TestBuildContext_NormalizesDiscordEmojiMarkup(t *testing.T) {
	cb := newTestContextBuilder()
	webhookID := snowflake.ID(9999)
	botID := snowflake.ID(42)

	messages := []discord.Message{
		{
			ID:        snowflake.ID(2),
			Content:   "Réponse <:criminel:1238422591547637800>",
			WebhookID: &webhookID,
			Author: discord.User{
				ID:       botID,
				Username: "bot",
			},
		},
		{
			ID:        snowflake.ID(1),
			Content:   "Salut <:criminel:1238422591547637800>",
			WebhookID: &webhookID,
			Author: discord.User{
				ID:       snowflake.ID(1),
				Username: "alice",
			},
		},
	}

	processed, err := cb.BuildContext(context.Background(), messages, snowflake.ID(100), botID)

	require.NoError(t, err)
	require.Len(t, processed.Messages, 2)
	assert.Contains(t, processed.Messages[0].Content, ":criminel:")
	assert.NotContains(t, processed.Messages[0].Content, "<:criminel:")
	assert.Equal(t, "Réponse :criminel:", processed.Messages[1].Content)
}

func TestBuildContext_PreservesImageRefsWithoutDescriptions(t *testing.T) {
	cb := newTestContextBuilder()
	webhookID := snowflake.ID(9999)
	botID := snowflake.ID(42)

	messages := []discord.Message{
		{
			ID:        snowflake.ID(1),
			Content:   "regarde",
			WebhookID: &webhookID,
			Attachments: []discord.Attachment{
				newAttachment(1, "https://example.com/image1.jpg", "image/jpeg", 800, 600, 1000000),
				newAttachment(2, "https://example.com/image2.png", "image/png", 1024, 768, 1500000),
			},
			Author: discord.User{
				ID:       snowflake.ID(1),
				Username: "alice",
			},
		},
	}

	processed, err := cb.BuildContext(context.Background(), messages, snowflake.ID(100), botID)

	require.NoError(t, err)
	require.Len(t, processed.Messages, 1)
	require.Len(t, processed.Images, 2)
	assert.Equal(t, []int{0, 1}, processed.Messages[0].ImageRefs)
	assert.Contains(t, processed.Messages[0].Content, "regarde")
	assert.NotContains(t, processed.Messages[0].Content, "<IMAGE_DESC>")
	assert.Equal(t, "1", processed.Images[0].MessageID)
	assert.Equal(t, "1", processed.Images[1].MessageID)
	assert.True(t, strings.HasPrefix(processed.Images[0].URL, "data:image/jpeg;base64,"))
	assert.Equal(t, "https://example.com/image1.jpg", processed.Images[0].SourceURL)
}

func TestBuildContext_KeepsImageOnlyMessages(t *testing.T) {
	cb := newTestContextBuilder()
	webhookID := snowflake.ID(9999)
	botID := snowflake.ID(42)

	messages := []discord.Message{
		{
			ID:        snowflake.ID(1),
			WebhookID: &webhookID,
			Attachments: []discord.Attachment{
				newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 800, 600, 1000000),
			},
			Author: discord.User{
				ID:       snowflake.ID(1),
				Username: "alice",
			},
		},
	}

	processed, err := cb.BuildContext(context.Background(), messages, snowflake.ID(100), botID)

	require.NoError(t, err)
	require.Len(t, processed.Messages, 1)
	require.Len(t, processed.Images, 1)
	assert.Equal(t, []int{0}, processed.Messages[0].ImageRefs)
	assert.NotContains(t, processed.Messages[0].Content, "<IMAGE_DESC>")
}

func TestBuildContext_StripsAttachmentURLsFromMessageContent(t *testing.T) {
	cb := newTestContextBuilder()
	webhookID := snowflake.ID(9999)
	botID := snowflake.ID(42)
	attachmentURL := "https://example.com/image.jpg"

	messages := []discord.Message{
		{
			ID:        snowflake.ID(1),
			Content:   "regarde " + attachmentURL,
			WebhookID: &webhookID,
			Attachments: []discord.Attachment{
				newAttachment(1, attachmentURL, "image/jpeg", 800, 600, 1000000),
			},
			Author: discord.User{
				ID:       snowflake.ID(1),
				Username: "alice",
			},
		},
	}

	processed, err := cb.BuildContext(context.Background(), messages, snowflake.ID(100), botID)

	require.NoError(t, err)
	require.Len(t, processed.Messages, 1)
	assert.Contains(t, processed.Messages[0].Content, "regarde")
	assert.NotContains(t, processed.Messages[0].Content, attachmentURL)
	require.Len(t, processed.Images, 1)
	assert.Equal(t, attachmentURL, processed.Images[0].SourceURL)
}

func newMessageWithAttachments(id snowflake.ID, content string, attachments []discord.Attachment) discord.Message {
	return discord.Message{
		ID:          id,
		Content:     content,
		Attachments: attachments,
		Author: discord.User{
			ID:       snowflake.ID(999),
			Username: "tester",
		},
	}
}

func newAttachment(id snowflake.ID, url, contentType string, width, height, size int) discord.Attachment {
	attachment := discord.Attachment{
		ID:          id,
		URL:         url,
		ContentType: ptr(contentType),
		Size:        size,
	}
	if width > 0 {
		attachment.Width = ptr(width)
	}
	if height > 0 {
		attachment.Height = ptr(height)
	}
	return attachment
}

func ptr[T any](v T) *T {
	return &v
}

func newTestContextBuilder() *ContextBuilder {
	cb := NewContextBuilder(nil, &mockProviderForTest{})
	cb.download = func(ctx context.Context, rawURL string, maxBytes int64) (*remoteContent, error) {
		return &remoteContent{
			SourceURL:   rawURL,
			FinalURL:    rawURL,
			ContentType: inferImageContentType(rawURL),
			Data:        []byte("image-bytes"),
		}, nil
	}
	return cb
}

func inferImageContentType(rawURL string) string {
	switch {
	case strings.HasSuffix(rawURL, ".png"):
		return "image/png"
	case strings.HasSuffix(rawURL, ".gif"):
		return "image/gif"
	case strings.HasSuffix(rawURL, ".webp"):
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

func singleFrameGIFData(t *testing.T) []byte {
	t.Helper()

	palette := color.Palette{color.Black, color.White}
	img := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	img.SetColorIndex(0, 0, 1)

	var buf bytes.Buffer
	err := gif.Encode(&buf, img, nil)
	require.NoError(t, err)
	return buf.Bytes()
}
