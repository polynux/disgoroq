package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"polynux/disgoroq/database"
)

const (
	attachmentCacheKindImageDescription = "image_description"
	attachmentCacheKindDocumentSummary  = "document_summary"
	defaultDocumentSummaryInstruction   = `Summarize the following document in markdown format.
Use headers, bullet points, and bold text for structure.
Capture key information concisely.`
)

type AttachmentCacheScope struct {
	Provider string
	Model    string
}

type cacheAwareProvider interface {
	AttachmentCache() database.AttachmentCache
	ChatCacheScopes() []AttachmentCacheScope
}

func attachmentCacheKey(input AttachmentCacheInput, provider, model string) database.AttachmentCacheKey {
	return database.AttachmentCacheKey{
		Kind:               input.Kind,
		AttachmentKey:      input.AttachmentKey,
		Provider:           provider,
		Model:              model,
		InstructionVersion: input.InstructionVersion,
	}
}

func imageDescriptionCacheInput(image ImageContext) AttachmentCacheInput {
	sourceURL := image.SourceURL
	if sourceURL == "" {
		sourceURL = image.URL
	}
	return AttachmentCacheInput{
		Kind:               attachmentCacheKindImageDescription,
		AttachmentKey:      fingerprintAttachment(sourceURL, image.Type, image.Size, image.Width, image.Height),
		SourceURL:          sourceURL,
		ContentType:        image.Type,
		SizeBytes:          image.Size,
		InstructionVersion: cacheInstructionVersion(attachmentCacheKindImageDescription, defaultVisionInstruction),
	}
}

func documentSummaryCacheInput(doc DocumentContext, maxSummaryTokens int) AttachmentCacheInput {
	return AttachmentCacheInput{
		Kind:               attachmentCacheKindDocumentSummary,
		AttachmentKey:      fingerprintAttachment(doc.URL, doc.Filename, doc.ContentType, doc.Size),
		SourceURL:          doc.URL,
		Filename:           doc.Filename,
		ContentType:        doc.ContentType,
		SizeBytes:          doc.Size,
		InstructionVersion: documentSummaryInstructionVersion(maxSummaryTokens),
	}
}

func documentSummaryInstructionVersion(maxSummaryTokens int) string {
	return cacheInstructionVersion(
		attachmentCacheKindDocumentSummary,
		fmt.Sprintf("%s\nMaximum length: %d tokens.", defaultDocumentSummaryInstruction, maxSummaryTokens),
	)
}

func cacheInstructionVersion(namespace, instruction string) string {
	sum := sha256.Sum256([]byte(instruction))
	return namespace + ":" + hex.EncodeToString(sum[:])
}

func fingerprintAttachment(parts ...any) string {
	hasher := sha256.New()
	for _, part := range parts {
		_, _ = hasher.Write([]byte(fmt.Sprintf("%v\x1f", part)))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
