package ai

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
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
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	require.NotNil(t, cb)
	assert.Equal(t, mockSession, cb.session)
	assert.Equal(t, mockProvider, cb.provider)
	assert.NotNil(t, cb.gifProcessor)
	assert.NotNil(t, cb.docProcessor)
	assert.Contains(t, cb.visionInstruction, "Décris cette image")
}

func TestGetImagesToProcess_NoImages(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:          "1",
			Content:     "Hello world",
			Attachments: []*discordgo.MessageAttachment{},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ValidImage(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "Check this image",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/image.jpg",
					ContentType: "image/jpeg",
					Width:       800,
					Height:      600,
					Size:        1000000,
				},
			},
		},
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
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "Here's a file",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/file.pdf",
					ContentType: "application/pdf",
					Size:        1000000,
				},
			},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_UnsupportedImageType(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "Here's an image",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/image.svg",
					ContentType: "image/svg+xml",
					Width:       800,
					Height:      600,
					Size:        1000000,
				},
			},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ImageTooLarge(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "Here's a large image",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/image.jpg",
					ContentType: "image/jpeg",
					Width:       800,
					Height:      600,
					Size:        25000000,
				},
			},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_ImageResolutionTooHigh(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "Here's a high res image",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/image.jpg",
					ContentType: "image/jpeg",
					Width:       6000,
					Height:      6000,
					Size:        5000000,
				},
			},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 0)
}

func TestGetImagesToProcess_MultipleImages(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "First message",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/image1.jpg",
					ContentType: "image/jpeg",
					Width:       800,
					Height:      600,
					Size:        1000000,
				},
				{
					ID:          "att2",
					URL:         "https://example.com/image2.png",
					ContentType: "image/png",
					Width:       1024,
					Height:      768,
					Size:        1500000,
				},
			},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 2)
	assert.Equal(t, "https://example.com/image1.jpg", images[0].url)
	assert.Equal(t, "https://example.com/image2.png", images[1].url)
}

func TestGetImagesToProcess_LimitToSixImages(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	attachments := make([]*discordgo.MessageAttachment, 0, 7)
	for i := 1; i <= 7; i++ {
		attachments = append(attachments, &discordgo.MessageAttachment{
			ID:          "att" + string(rune('0'+i)),
			URL:         "https://example.com/image" + string(rune('0'+i)) + ".jpg",
			ContentType: "image/jpeg",
			Width:       800,
			Height:      600,
			Size:        1000000,
		})
	}

	messages := []*discordgo.Message{
		{
			ID:          "1",
			Content:     "Many images",
			Attachments: attachments,
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Equal(t, 6, len(images))
}

func TestGetImagesToProcess_ReversedOrder(t *testing.T) {
	mockSession := &discordgo.Session{}
	mockProvider := &mockProviderForTest{}

	cb := NewContextBuilder(mockSession, mockProvider)

	messages := []*discordgo.Message{
		{
			ID:      "1",
			Content: "First",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att1",
					URL:         "https://example.com/first.jpg",
					ContentType: "image/jpeg",
					Width:       800,
					Height:      600,
					Size:        1000000,
				},
			},
		},
		{
			ID:      "2",
			Content: "Second",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att2",
					URL:         "https://example.com/second.jpg",
					ContentType: "image/jpeg",
					Width:       800,
					Height:      600,
					Size:        1000000,
				},
			},
		},
	}

	images := cb.getImagesToProcess(context.Background(), messages)
	require.Len(t, images, 2)
	assert.Equal(t, "2", images[0].id, "Should process images in reverse order")
	assert.Equal(t, "1", images[1].id, "Should process images in reverse order")
}
