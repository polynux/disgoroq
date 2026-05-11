package ai

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/young2j/oxmltotext/docxtotext"
	"github.com/young2j/oxmltotext/pptxtotext"
	"github.com/young2j/oxmltotext/xlsxtotext"
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
	extensions := map[string]string{
		".pdf":  "pdf",
		".docx": "docx",
		".xlsx": "xlsx",
		".pptx": "pptx",
		".txt":  "txt",
		".csv":  "csv",
		".md":   "md",
	}

	for ext, format := range extensions {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return format
		}
	}
	return ""
}

// extractText extracts text from a document based on its format
func (dp *DocumentProcessor) extractText(ctx context.Context, docURL, format string) (string, error) {
	data, err := dp.downloadDocument(ctx, docURL)
	if err != nil {
		return "", err
	}

	switch format {
	case "pdf":
		return dp.extractPDF(data)
	case "docx":
		return dp.extractDOCX(data)
	case "xlsx":
		return dp.extractXLSX(data)
	case "pptx":
		return dp.extractPPTX(data)
	case "txt":
		return dp.extractTXT(data)
	case "csv":
		return dp.extractCSV(data)
	case "md":
		return dp.extractMarkdown(data)
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
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

// downloadDocument downloads a document from URL
func (dp *DocumentProcessor) downloadDocument(ctx context.Context, docURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", docURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download document: status %d", resp.StatusCode)
	}

	if resp.ContentLength > int64(dp.maxSizeMB)*1024*1024 {
		return nil, fmt.Errorf("document too large: %d MB > %d MB limit",
			resp.ContentLength/1024/1024, dp.maxSizeMB)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read document: %w", err)
	}

	if len(data) > dp.maxSizeMB*1024*1024 {
		return nil, fmt.Errorf("document too large: %d MB > %d MB limit",
			len(data)/1024/1024, dp.maxSizeMB)
	}

	return data, nil
}

// extractPDF extracts text from PDF data
func (dp *DocumentProcessor) extractPDF(data []byte) (string, error) {
	r := bytes.NewReader(data)
	pdfReader, err := pdf.NewReader(r, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}

	var text strings.Builder
	numPages := pdfReader.NumPage()

	for pageNum := 1; pageNum <= numPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		rows, err := page.GetTextByRow()
		if err != nil {
			continue
		}

		for _, row := range rows {
			for _, word := range row.Content {
				text.WriteString(word.S)
				text.WriteString(" ")
			}
			text.WriteString("\n")
		}
	}

	return text.String(), nil
}

// extractDOCX extracts text from DOCX data
func (dp *DocumentProcessor) extractDOCX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	doc, err := docxtotext.OpenReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open DOCX: %w", err)
	}
	defer doc.Close()

	text, err := doc.ExtractTexts()
	if err != nil {
		return "", fmt.Errorf("failed to extract DOCX text: %w", err)
	}

	return text, nil
}

// extractXLSX extracts text from XLSX data
func (dp *DocumentProcessor) extractXLSX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	xlsx, err := xlsxtotext.OpenReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open XLSX: %w", err)
	}
	defer xlsx.Close()

	text, err := xlsx.ExtractTexts()
	if err != nil {
		return "", fmt.Errorf("failed to extract XLSX text: %w", err)
	}

	return text, nil
}

// extractPPTX extracts text from PPTX data
func (dp *DocumentProcessor) extractPPTX(data []byte) (string, error) {
	reader := bytes.NewReader(data)
	pptx, err := pptxtotext.OpenReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open PPTX: %w", err)
	}
	defer pptx.Close()

	text, err := pptx.ExtractTexts()
	if err != nil {
		return "", fmt.Errorf("failed to extract PPTX text: %w", err)
	}

	return text, nil
}

func (dp *DocumentProcessor) extractTXT(data []byte) (string, error) {
	return string(data), nil
}

func (dp *DocumentProcessor) extractCSV(data []byte) (string, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	records, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("failed to parse CSV: %w", err)
	}

	var result strings.Builder
	for i, record := range records {
		if i >= 100 {
			result.WriteString("\n... [truncated after 100 rows]")
			break
		}
		result.WriteString(strings.Join(record, " | "))
		result.WriteString("\n")
	}

	return result.String(), nil
}

func (dp *DocumentProcessor) extractMarkdown(data []byte) (string, error) {
	return string(data), nil
}
