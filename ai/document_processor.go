package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
)

// DocumentProcessor handles document text extraction and summarization
type DocumentProcessor struct {
	maxSizeMB        int
	maxSummaryTokens int
	summaryModel     string
	provider         Provider
	cache            database.AttachmentCache
	chatCacheScopes  []AttachmentCacheScope
}

type DocumentProcessorConfig struct {
	MaxSizeMB        int
	MaxSummaryTokens int
	SummaryModel     string
}

// supportedDocuments maps content types to format identifiers
var supportedDocuments = map[string]string{
	"application/pdf": "pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"text/plain":    "txt",
	"text/csv":      "csv",
	"text/markdown": "md",
}

// NewDocumentProcessor creates a new DocumentProcessor with explicit runtime configuration.
func NewDocumentProcessor(provider Provider, cfg DocumentProcessorConfig) *DocumentProcessor {
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 50
	}
	if cfg.MaxSummaryTokens <= 0 {
		cfg.MaxSummaryTokens = 500
	}
	if cfg.SummaryModel == "" {
		cfg.SummaryModel = "llama-3.1-8b-instant"
	}

	return &DocumentProcessor{
		maxSizeMB:        cfg.MaxSizeMB,
		maxSummaryTokens: cfg.MaxSummaryTokens,
		summaryModel:     cfg.SummaryModel,
		provider:         provider,
		cache: func() database.AttachmentCache {
			if cacheProvider, ok := provider.(cacheAwareProvider); ok {
				return cacheProvider.AttachmentCache()
			}
			return nil
		}(),
		chatCacheScopes: func() []AttachmentCacheScope {
			if cacheProvider, ok := provider.(cacheAwareProvider); ok {
				return cacheProvider.ChatCacheScopes()
			}
			return nil
		}(),
	}
}

// CanProcess checks if the content type is supported
func (dp *DocumentProcessor) CanProcess(contentType string) bool {
	_, supported := supportedDocuments[contentType]
	return supported
}

// ProcessDocument processes a document and returns a markdown summary
func (dp *DocumentProcessor) ProcessDocument(ctx context.Context, doc DocumentContext) (string, error) {
	format := dp.getFormatFromFilename(doc.Filename)
	if format == "" {
		return "", fmt.Errorf("unsupported document format: %s", doc.Filename)
	}

	if summary, ok := dp.getCachedSummary(ctx, doc); ok {
		return summary, nil
	}

	text, err := dp.extractText(ctx, doc.URL, format)
	if err != nil {
		return "", fmt.Errorf("failed to extract text: %w", err)
	}

	summary, err := dp.summarizeText(ctx, text, doc)
	if err != nil {
		return fmt.Sprintf("[Document: %s]", doc.Filename), nil
	}

	return summary, nil
}

func (dp *DocumentProcessor) getFormatFromFilename(filename string) string {
	return detectDocumentFormat(filename, "")
}

// extractText extracts text from a document based on its format
func (dp *DocumentProcessor) extractText(ctx context.Context, docURL, format string) (string, error) {
	content, err := downloadRemoteContent(ctx, http.DefaultClient, docURL, int64(dp.maxSizeMB)*1024*1024)
	if err != nil {
		return "", err
	}

	return extractDocumentText(format, content.Data)
}

// summarizeText summarizes extracted text using AI
func (dp *DocumentProcessor) summarizeText(ctx context.Context, text string, doc DocumentContext) (string, error) {
	if len(text) > 12000 {
		text = text[:12000] + "\n...[truncated]"
	}

	prompt := buildDocumentSummaryPrompt(doc.Filename, text, dp.maxSummaryTokens)

	req := &ChatRequest{
		Model:           dp.summaryModel,
		Messages:        []Message{{Role: "user", Content: prompt}},
		MaxTokens:       dp.maxSummaryTokens,
		Temperature:     0.3,
		AttachmentCache: cacheInputPtr(documentSummaryCacheInput(doc, dp.maxSummaryTokens)),
	}

	resp, err := dp.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to summarize: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return "", fmt.Errorf("empty document summary response")
	}

	return summary, nil
}

func buildDocumentSummaryPrompt(filename, text string, maxSummaryTokens int) string {
	return fmt.Sprintf(`%s
Maximum length: %d tokens.

Document: %s

Content:
%s`, defaultDocumentSummaryInstruction, maxSummaryTokens, filename, text)
}

func (dp *DocumentProcessor) getCachedSummary(ctx context.Context, doc DocumentContext) (string, bool) {
	if dp.cache == nil || len(dp.chatCacheScopes) == 0 {
		return "", false
	}

	input := documentSummaryCacheInput(doc, dp.maxSummaryTokens)
	for _, scope := range dp.chatCacheScopes {
		if scope.Provider == "" || scope.Model == "" {
			continue
		}

		entry, found, err := dp.cache.GetAttachmentCache(ctx, attachmentCacheKey(input, scope.Provider, scope.Model))
		if err != nil {
			logger.Warn("Failed to read document summary cache",
				zap.Error(err),
				zap.String("provider", scope.Provider),
				zap.String("model", scope.Model),
				zap.String("filename", doc.Filename))
			continue
		}
		if !found {
			continue
		}

		summary := strings.TrimSpace(entry.Content)
		if summary == "" {
			continue
		}

		logger.Debug("Using cached document summary",
			zap.String("provider", scope.Provider),
			zap.String("model", scope.Model),
			zap.String("filename", doc.Filename))

		return summary, true
	}

	return "", false
}

func cacheInputPtr(input AttachmentCacheInput) *AttachmentCacheInput {
	return &input
}
