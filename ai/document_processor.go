package ai

import (
	"context"
)

// DocumentProcessor handles document text extraction and summarization
type DocumentProcessor struct {
	maxSizeMB        int
	maxSummaryTokens int
	summaryModel     string
	provider         Provider
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

// NewDocumentProcessor creates a new DocumentProcessor with default configuration
func NewDocumentProcessor(provider Provider) *DocumentProcessor {
	return &DocumentProcessor{
		maxSizeMB:        50,
		maxSummaryTokens: 500,
		summaryModel:     "llama-3.1-8b-instant",
		provider:         provider,
	}
}

// CanProcess checks if the content type is supported
func (dp *DocumentProcessor) CanProcess(contentType string) bool {
	_, supported := supportedDocuments[contentType]
	return supported
}

// ProcessDocument processes a document and returns a markdown summary
func (dp *DocumentProcessor) ProcessDocument(ctx context.Context, docURL, filename string) (string, error) {
	// TODO: Implement in Tasks 2-6
	return "", nil
}

// extractText extracts text from a document based on its format
func (dp *DocumentProcessor) extractText(ctx context.Context, docURL, format string) (string, error) {
	// TODO: Implement in Tasks 2-5
	return "", nil
}

// summarizeText summarizes extracted text using AI
func (dp *DocumentProcessor) summarizeText(ctx context.Context, text, filename string) (string, error) {
	// TODO: Implement in Task 6
	return "", nil
}

// downloadDocument downloads a document from URL
func (dp *DocumentProcessor) downloadDocument(ctx context.Context, docURL string) ([]byte, error) {
	// TODO: Implement
	return nil, nil
}
