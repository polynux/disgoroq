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

	"polynux/disgoroq/logger"
)

type ContextBuilder struct {
	client            *bot.Client
	provider          Provider
	visionInstruction string
	gifProcessor      *GIFProcessor
	docProcessor      *DocumentProcessor
}

func NewContextBuilder(client *bot.Client, provider Provider) *ContextBuilder {
	return &ContextBuilder{
		client:            client,
		provider:          provider,
		visionInstruction: "Décris cette image en 3-4 phrases ultra-courtes (max 5 mots chacune) qui capturent l'essentiel de la scène. UNIQUEMENT LES PHRASES. UNE PAR LIGNE.",
		gifProcessor:      NewGIFProcessor(),
		docProcessor:      NewDocumentProcessor(provider, DocumentProcessorConfig{}),
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
	describedImages := cb.processImages(ctx, imagesToProcess)
	documentSummaries := cb.getDocumentSummaries(ctx, messages)

	formattedMessages := make([]Message, 0, len(messages))
	imageContexts := make([]ImageContext, 0)
	memberCache := make(map[snowflake.ID]*discord.Member)

	for idx := len(messages) - 1; idx >= 0; idx-- {
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

		imageDescription := ""
		imageRef := -1
		if desc, found := describedImages[messages[idx].ID.String()]; found {
			imageDescription = desc
			for _, img := range imagesToProcess {
				if img.id == messages[idx].ID.String() {
					imageRef = len(imageContexts)
					imageContexts = append(imageContexts, ImageContext{
						MessageID: img.id,
						URL:       img.url,
						Type:      img.contentType,
						Width:     img.width,
						Height:    img.height,
						Size:      img.size,
					})
					break
				}
			}
		} else {
			if messages[idx].Content == "" {
				continue
			}
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

		if imageDescription != "" {
			content.WriteString("<IMAGE_DESC>\n")
			content.WriteString(strings.ReplaceAll(imageDescription, "\n", ""))
			content.WriteString("</IMAGE_DESC>\n")
		}

		if docSummary, exists := documentSummaries[messages[idx].ID.String()]; exists {
			content.WriteString("[Document Summary]\n")
			content.WriteString(docSummary)
			content.WriteString("\n\n")
		}

		content.WriteString(messages[idx].Content)
		content.WriteString("\n\n")

		imageRefs := []int{}
		if imageRef != -1 {
			imageRefs = append(imageRefs, imageRef)
		}

		if messages[idx].Author.ID == botID {
			formattedMessages = append(formattedMessages, Message{
				Role:       "assistant",
				Content:    messages[idx].Content,
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

type processedImage struct {
	id          string
	description string
}

func (cb *ContextBuilder) processImages(ctx context.Context, imagesToProcess []imageToProcess) map[string]string {
	describedImages := make(map[string]string)

	ch := make(chan processedImage, len(imagesToProcess))

	for _, img := range imagesToProcess {
		go func(img imageToProcess) {
			response, err := cb.provider.Vision(ctx, &VisionRequest{
				Instruction: cb.visionInstruction,
				ImageURL:    img.url,
				ImageType:   img.contentType,
				Width:       img.width,
				Height:      img.height,
				MaxTokens:   100,
				Temperature: 0.2,
			})
			if err != nil {
				logger.Error("Error getting image description",
					zap.Error(err),
					zap.String("image_url", img.url),
				)
				ch <- processedImage{
					id:          img.id,
					description: "",
				}
				return
			}
			ch <- processedImage{
				id:          img.id,
				description: response.Description,
			}
		}(img)
	}

	for range imagesToProcess {
		img := <-ch
		if img.description == "" {
			continue
		}
		describedImages[img.id] = img.description
	}

	return describedImages
}
