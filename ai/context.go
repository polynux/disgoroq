package ai

import (
	"context"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"
	"slices"
	"strings"

	"polynux/disgoroq/emoji"
	"polynux/disgoroq/logger"
)

type ContextBuilder struct {
	client       *bot.Client
	gifProcessor *GIFProcessor
	docProcessor *DocumentProcessor
}

func NewContextBuilder(client *bot.Client, provider Provider) *ContextBuilder {
	return &ContextBuilder{
		client:       client,
		gifProcessor: NewGIFProcessor(),
		docProcessor: NewDocumentProcessor(provider, DocumentProcessorConfig{}),
	}
}

type ProcessedMessage struct {
	Messages []Message
	Images   []ImageContext
}

var supportedImageTypes = []string{
	"image/jpeg",
	"image/png",
	"image/jpg",
	"image/gif",
	"image/webp",
}

func (cb *ContextBuilder) BuildContext(ctx context.Context, messages []discord.Message, guildID snowflake.ID, botID snowflake.ID) (*ProcessedMessage, error) {
	imagesToProcess := cb.getImagesToProcess(ctx, messages)
	documentSummaries := cb.getDocumentSummaries(ctx, messages)
	imageContexts, imageRefsByMessage := buildImageContexts(imagesToProcess)

	formattedMessages := make([]Message, 0, len(messages))
	memberCache := make(map[snowflake.ID]*discord.Member)

	for idx := len(messages) - 1; idx >= 0; idx-- {
		normalizedContent := emoji.NormalizeDiscordEmojiShortcodes(messages[idx].Content)

		if strings.Contains(messages[idx].Content, "Horoscope du jour:") && messages[idx].Author.ID == botID {
			idx--
			if idx < 0 {
				break
			}
			continue
		}
		if messages[idx].Content == "(et je parle de sexe evidemment)" && messages[idx].Author.ID == botID {
			continue
		}

		imageRefs := imageRefsByMessage[messages[idx].ID.String()]
		docSummary, hasDocSummary := documentSummaries[messages[idx].ID.String()]
		if normalizedContent == "" && !hasDocSummary && len(imageRefs) == 0 {
			continue
		}

		var nick string
		// Check if message is from a webhook (webhooks aren't guild members)
		if messages[idx].WebhookID != nil {
			nick = messages[idx].Author.Username
		} else if cachedMember, exists := memberCache[messages[idx].Author.ID]; exists {
			// Use cached member (nil means we already tried and failed)
			if cachedMember != nil {
				nick = ""
				if cachedMember.Nick != nil {
					nick = *cachedMember.Nick
				}
				if nick == "" {
					nick = messages[idx].Author.Username
				}
			} else {
				nick = messages[idx].Author.Username
			}
		} else {
			// Try to fetch guild member
			userMember, err := cb.client.Rest.GetMember(guildID, messages[idx].Author.ID, rest.WithCtx(ctx))
			if err != nil {
				// Log warning and fallback to username for non-members (webhooks, cross-server announcements)
				logger.Warn("Could not get guild member, using username",
					zap.Error(err),
					zap.String("user_id", messages[idx].Author.ID.String()),
					zap.String("guild_id", guildID.String()),
				)
				nick = messages[idx].Author.Username
				// Cache nil to avoid repeated failed lookups
				memberCache[messages[idx].Author.ID] = nil
			} else {
				memberCache[messages[idx].Author.ID] = userMember
				nick = ""
				if userMember.Nick != nil {
					nick = *userMember.Nick
				}
				if nick == "" {
					nick = messages[idx].Author.Username
				}
			}
		}

		var content strings.Builder
		content.WriteString("<@")
		content.WriteString(messages[idx].Author.ID.String())
		content.WriteString(">")
		content.WriteString(nick)
		content.WriteString(": ")

		if hasDocSummary {
			content.WriteString("[Document Summary]\n")
			content.WriteString(docSummary)
			content.WriteString("\n\n")
		}

		content.WriteString(normalizedContent)
		content.WriteString("\n\n")

		if messages[idx].Author.ID == botID {
			formattedMessages = append(formattedMessages, Message{
				Role:       "assistant",
				Content:    normalizedContent,
				AuthorID:   messages[idx].Author.ID.String(),
				AuthorNick: nick,
				MessageID:  messages[idx].ID.String(),
				ImageRefs:  imageRefs,
			})
		} else {
			formattedMessages = append(formattedMessages, Message{
				Role:       "user",
				Content:    content.String(),
				AuthorID:   messages[idx].Author.ID.String(),
				AuthorNick: nick,
				MessageID:  messages[idx].ID.String(),
				ImageRefs:  imageRefs,
			})
		}
	}

	return &ProcessedMessage{
		Messages: formattedMessages,
		Images:   imageContexts,
	}, nil
}

func buildImageContexts(imagesToProcess []imageToProcess) ([]ImageContext, map[string][]int) {
	imageContexts := make([]ImageContext, 0, len(imagesToProcess))
	imageRefsByMessage := make(map[string][]int, len(imagesToProcess))

	for _, img := range imagesToProcess {
		ref := len(imageContexts)
		imageContexts = append(imageContexts, ImageContext{
			MessageID: img.id,
			URL:       img.url,
			Type:      img.contentType,
			Width:     img.width,
			Height:    img.height,
			Size:      img.size,
		})
		imageRefsByMessage[img.id] = append(imageRefsByMessage[img.id], ref)
	}

	return imageContexts, imageRefsByMessage
}

type imageToProcess struct {
	id          string
	url         string
	contentType string
	width       int
	height      int
	size        int64
}

func (cb *ContextBuilder) getImagesToProcess(ctx context.Context, messages []discord.Message) []imageToProcess {
	attachmentCount := 0
	imagesToProcess := make([]imageToProcess, 0)
	for idx := len(messages) - 1; idx >= 0; idx-- {
		for _, attachment := range messages[idx].Attachments {
			if attachmentCount > 5 {
				return imagesToProcess
			}
			// ContentType is a pointer
			if attachment.ContentType == nil {
				continue
			}
			if !strings.HasPrefix(*attachment.ContentType, "image/") {
				continue
			}
			if !slices.Contains(supportedImageTypes, *attachment.ContentType) {
				continue
			}
			if attachment.Size > 20000000 {
				continue
			}
			// Width and Height are pointers
			if attachment.Width != nil && attachment.Height != nil {
				if *attachment.Width**attachment.Height > 33000000 {
					continue
				}
			}

			// Process animated GIFs
			url := attachment.URL
			contentType := *attachment.ContentType
			if *attachment.ContentType == "image/gif" && cb.gifProcessor != nil {
				base64Grid, err := cb.gifProcessor.ProcessGIF(ctx, attachment.URL)
				if err == nil && base64Grid != "" {
					// Replace with base64 data URI
					url = "data:image/jpeg;base64," + base64Grid
					contentType = "image/jpeg"
				}
			}

			width := 0
			if attachment.Width != nil {
				width = *attachment.Width
			}
			height := 0
			if attachment.Height != nil {
				height = *attachment.Height
			}

			imagesToProcess = append(imagesToProcess, imageToProcess{
				id:          messages[idx].ID.String(),
				url:         url,
				contentType: contentType,
				width:       width,
				height:      height,
				size:        int64(attachment.Size),
			})
			attachmentCount++
		}
	}

	return imagesToProcess
}

func (cb *ContextBuilder) getDocumentSummaries(ctx context.Context, messages []discord.Message) map[string]string {
	summaries := make(map[string]string)

	for idx := len(messages) - 1; idx >= 0; idx-- {
		for _, attachment := range messages[idx].Attachments {
			if attachment.ContentType == nil {
				continue
			}
			if cb.docProcessor != nil && cb.docProcessor.CanProcess(*attachment.ContentType) {
				summary, err := cb.docProcessor.ProcessDocument(ctx, attachment.URL, attachment.Filename)
				if err == nil && summary != "" {
					summaries[messages[idx].ID.String()] = summary
				}
			}
		}
	}

	return summaries
}
