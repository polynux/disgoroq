package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/young2j/oxmltotext/docxtotext"
	"github.com/young2j/oxmltotext/pptxtotext"
	"github.com/young2j/oxmltotext/xlsxtotext"
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
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

// summarizeText summarizes extracted text using AI
func (dp *DocumentProcessor) summarizeText(ctx context.Context, text, filename string) (string, error) {
	// TODO: Implement in Task 6
	return "", nil
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
