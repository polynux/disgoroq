package ai

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
)

const defaultVisionInstruction = "Décris cette image en 3-4 phrases ultra-courtes (max 5 mots chacune) qui capturent l'essentiel de la scène. UNIQUEMENT LES PHRASES. UNE PAR LIGNE."

// InlineImageProvider is implemented by providers that can decide whether a
// chat model should receive referenced images inline instead of through a
// separate vision-to-text step.
type InlineImageProvider interface {
	SupportsInlineImages(chatModel, visionModel string) bool
}

func supportsInlineImages(provider Provider, chatModel, visionModel string) bool {
	inlineProvider, ok := provider.(InlineImageProvider)
	if !ok {
		return false
	}

	return inlineProvider.SupportsInlineImages(chatModel, visionModel)
}

func resolveImageRefs(images []ImageContext, refs []int) []ImageContext {
	resolved := make([]ImageContext, 0, len(refs))
	for _, ref := range refs {
		if ref < 0 || ref >= len(images) {
			continue
		}
		resolved = append(resolved, images[ref])
	}
	return resolved
}

func (r *RetryWrapper) prepareChatRequest(ctx context.Context, req *ChatRequest) *ChatRequest {
	if len(req.Images) == 0 || supportsInlineImages(r.provider, req.Model, r.visionModel) {
		return req
	}

	adapted := *req
	adapted.Messages = cloneMessages(req.Messages)
	adapted.Images = nil

	descriptions := r.describeImagesForChat(ctx, req.Images)
	for idx := range adapted.Messages {
		if len(adapted.Messages[idx].ImageRefs) == 0 {
			continue
		}

		adapted.Messages[idx].Content = prependImageDescriptions(
			adapted.Messages[idx].Content,
			descriptionsForRefs(adapted.Messages[idx].ImageRefs, descriptions),
		)
		adapted.Messages[idx].ImageRefs = nil
	}

	return &adapted
}

func cloneMessages(messages []Message) []Message {
	cloned := make([]Message, len(messages))
	for idx, msg := range messages {
		cloned[idx] = msg
		if len(msg.ImageRefs) > 0 {
			cloned[idx].ImageRefs = append([]int(nil), msg.ImageRefs...)
		}
	}
	return cloned
}

func (r *RetryWrapper) describeImagesForChat(ctx context.Context, images []ImageContext) map[int]string {
	descriptions := make(map[int]string, len(images))
	for idx, image := range images {
		if description, ok := r.getCachedImageDescription(ctx, image); ok {
			descriptions[idx] = description
			continue
		}

		response, err := r.Vision(ctx, &VisionRequest{
			Instruction: defaultVisionInstruction,
			ImageURL:    image.URL,
			ImageType:   image.Type,
			Width:       image.Width,
			Height:      image.Height,
			MaxTokens:   100,
			Temperature: 0.2,
		})
		if err != nil {
			logger.Warn("Failed to describe image for chat fallback",
				zap.Error(err),
				zap.String("provider", r.provider.Name()),
				zap.String("image_url", image.URL),
				zap.String("chat_model", r.chatModel),
				zap.String("vision_model", r.visionModel))
			continue
		}

		description := strings.TrimSpace(response.Description)
		if description == "" {
			continue
		}

		r.storeCachedImageDescription(ctx, image, description)
		descriptions[idx] = description
	}

	return descriptions
}

func (r *RetryWrapper) getCachedImageDescription(ctx context.Context, image ImageContext) (string, bool) {
	if r.cache == nil {
		return "", false
	}

	entry, found, err := r.cache.GetAttachmentCache(ctx, attachmentCacheKey(imageDescriptionCacheInput(image), r.provider.Name(), r.visionModel))
	if err != nil {
		logger.Warn("Failed to read image description cache",
			zap.Error(err),
			zap.String("provider", r.provider.Name()),
			zap.String("model", r.visionModel),
			zap.String("image_url", image.URL))
		return "", false
	}
	if !found {
		return "", false
	}

	description := strings.TrimSpace(entry.Content)
	if description == "" {
		return "", false
	}

	logger.Debug("Using cached image description",
		zap.String("provider", r.provider.Name()),
		zap.String("model", r.visionModel),
		zap.String("image_url", image.URL))

	return description, true
}

func (r *RetryWrapper) storeCachedImageDescription(ctx context.Context, image ImageContext, description string) {
	if r.cache == nil {
		return
	}

	input := imageDescriptionCacheInput(image)
	if err := r.cache.PutAttachmentCache(ctx, database.AttachmentCacheEntry{
		AttachmentCacheKey: attachmentCacheKey(input, r.provider.Name(), r.visionModel),
		Content:            description,
		SourceURL:          input.SourceURL,
		ContentType:        input.ContentType,
		SizeBytes:          input.SizeBytes,
	}); err != nil {
		logger.Warn("Failed to write image description cache",
			zap.Error(err),
			zap.String("provider", r.provider.Name()),
			zap.String("model", r.visionModel),
			zap.String("image_url", image.URL))
	}
}

func descriptionsForRefs(refs []int, descriptions map[int]string) []string {
	resolved := make([]string, 0, len(refs))
	for _, ref := range refs {
		description, ok := descriptions[ref]
		if !ok {
			continue
		}

		description = strings.TrimSpace(description)
		if description == "" {
			continue
		}

		resolved = append(resolved, strings.ReplaceAll(description, "\n", " "))
	}
	return resolved
}

func prependImageDescriptions(content string, descriptions []string) string {
	if len(descriptions) == 0 {
		return content
	}

	var builder strings.Builder
	builder.WriteString("<IMAGE_DESC>\n")
	builder.WriteString(strings.Join(descriptions, "\n"))
	builder.WriteString("\n</IMAGE_DESC>\n")
	builder.WriteString(content)
	return builder.String()
}
