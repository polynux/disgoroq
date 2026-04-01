package ai

import (
	"context"
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
	assert.Equal(t, mockProvider, cb.provider)
	assert.NotNil(t, cb.gifProcessor)
	assert.NotNil(t, cb.docProcessor)
	assert.Contains(t, cb.visionInstruction, "Décris cette image")
}

func TestGetImagesToProcess_NoImages(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

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
	cb := NewContextBuilder(nil, &mockProviderForTest{})

	messages := []discord.Message{
		newMessageWithAttachments(1, "Check this image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 1)
	assert.Equal(t, "1", images[0].id)
	assert.Equal(t, "https://example.com/image.jpg", images[0].url)
	assert.Equal(t, "image/jpeg", images[0].contentType)
	assert.Equal(t, 800, images[0].width)
	assert.Equal(t, 600, images[0].height)
	assert.Equal(t, int64(1000000), images[0].size)
}

func TestGetImagesToProcess_NonImageAttachment(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's a file", []discord.Attachment{
			newAttachment(1, "https://example.com/file.pdf", "application/pdf", 0, 0, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_UnsupportedImageType(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's an image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.svg", "image/svg+xml", 800, 600, 1000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ImageTooLarge(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's a large image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 800, 600, 25000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ImageResolutionTooHigh(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

	messages := []discord.Message{
		newMessageWithAttachments(1, "Here's a high res image", []discord.Attachment{
			newAttachment(1, "https://example.com/image.jpg", "image/jpeg", 6000, 6000, 5000000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_MultipleImages(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

	messages := []discord.Message{
		newMessageWithAttachments(1, "First message", []discord.Attachment{
			newAttachment(1, "https://example.com/image1.jpg", "image/jpeg", 800, 600, 1000000),
			newAttachment(2, "https://example.com/image2.png", "image/png", 1024, 768, 1500000),
		}),
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 2)
	assert.Equal(t, "https://example.com/image1.jpg", images[0].url)
	assert.Equal(t, "https://example.com/image2.png", images[1].url)
}

func TestGetImagesToProcess_LimitToSixImages(t *testing.T) {
	cb := NewContextBuilder(nil, &mockProviderForTest{})

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
	cb := NewContextBuilder(nil, &mockProviderForTest{})

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
