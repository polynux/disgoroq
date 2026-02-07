package ai

import (
	"context"
	"slices"
	"strings"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

type ContextBuilder struct {
	session           *discordgo.Session
	provider          Provider
	visionInstruction string
}

func NewContextBuilder(session *discordgo.Session, provider Provider) *ContextBuilder {
	return &ContextBuilder{
		session:           session,
		provider:          provider,
		visionInstruction: "Décris cette image en 3-4 phrases ultra-courtes (max 5 mots chacune) qui capturent l'essentiel de la scène. UNIQUEMENT LES PHRASES. UNE PAR LIGNE.",
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

func (cb *ContextBuilder) BuildContext(ctx context.Context, messages []*discordgo.Message, guildID string, botID string) (*ProcessedMessage, error) {
	imagesToProcess := cb.getImagesToProcess(messages)
	describedImages := cb.processImages(ctx, imagesToProcess)

	formattedMessages := make([]Message, 0, len(messages))
	imageContexts := make([]ImageContext, 0)
	memberCache := make(map[string]*discordgo.Member)

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
		if desc, found := describedImages[messages[idx].ID]; found {
			imageDescription = desc
			for _, img := range imagesToProcess {
				if img.id == messages[idx].ID {
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
		if messages[idx].WebhookID != "" {
			nick = messages[idx].Author.Username
		} else if cachedMember, exists := memberCache[messages[idx].Author.ID]; exists {
			// Use cached member (nil means we already tried and failed)
			if cachedMember != nil {
				nick = cachedMember.Nick
				if nick == "" {
					nick = messages[idx].Author.Username
				}
			} else {
				nick = messages[idx].Author.Username
			}
		} else {
			// Try to fetch guild member
			userMember, err := cb.session.GuildMember(guildID, messages[idx].Author.ID)
			if err != nil {
				// Log warning and fallback to username for non-members (webhooks, cross-server announcements)
				logger.Warn("Could not get guild member, using username",
					zap.Error(err),
					zap.String("user_id", messages[idx].Author.ID),
					zap.String("guild_id", guildID),
				)
				nick = messages[idx].Author.Username
				// Cache nil to avoid repeated failed lookups
				memberCache[messages[idx].Author.ID] = nil
			} else {
				memberCache[messages[idx].Author.ID] = userMember
				nick = userMember.Nick
				if nick == "" {
					nick = messages[idx].Author.Username
				}
			}
		}

		var content strings.Builder
		content.WriteString("<@")
		content.WriteString(messages[idx].Author.ID)
		content.WriteString(">")
		content.WriteString(nick)
		content.WriteString(": ")

		if imageDescription != "" {
			content.WriteString("<IMAGE_DESC>\n")
			content.WriteString(strings.ReplaceAll(imageDescription, "\n", ""))
			content.WriteString("</IMAGE_DESC>\n")
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
				AuthorID:   messages[idx].Author.ID,
				AuthorNick: nick,
				MessageID:  messages[idx].ID,
				ImageRefs:  imageRefs,
			})
		} else {
			formattedMessages = append(formattedMessages, Message{
				Role:       "user",
				Content:    content.String(),
				AuthorID:   messages[idx].Author.ID,
				AuthorNick: nick,
				MessageID:  messages[idx].ID,
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

func (cb *ContextBuilder) getImagesToProcess(messages []*discordgo.Message) []imageToProcess {
	attachmentCount := 0
	imagesToProcess := make([]imageToProcess, 0)
	for idx := len(messages) - 1; idx >= 0; idx-- {
		for _, attachment := range messages[idx].Attachments {
			if attachmentCount > 5 {
				return imagesToProcess
			}
			if !strings.HasPrefix(attachment.ContentType, "image/") {
				continue
			}
			if !slices.Contains(supportedImageTypes, attachment.ContentType) {
				continue
			}
			if attachment.Size > 20000000 {
				continue
			}
			if attachment.Width*attachment.Height > 33000000 {
				continue
			}
			imagesToProcess = append(imagesToProcess, imageToProcess{
				id:          messages[idx].ID,
				url:         attachment.URL,
				contentType: attachment.ContentType,
				width:       attachment.Width,
				height:      attachment.Height,
				size:        int64(attachment.Size),
			})
			attachmentCount++
		}
	}

	return imagesToProcess
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
