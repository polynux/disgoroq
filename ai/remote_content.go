package ai

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/ledongthuc/pdf"
	"github.com/young2j/oxmltotext/docxtotext"
	"github.com/young2j/oxmltotext/pptxtotext"
	"github.com/young2j/oxmltotext/xlsxtotext"
	"golang.org/x/net/html"
)

type remoteContent struct {
	SourceURL   string
	FinalURL    string
	Filename    string
	ContentType string
	Data        []byte
}

func downloadRemoteContent(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) (*remoteContent, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("max bytes must be positive")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download content: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download content: status %d", resp.StatusCode)
	}

	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("content too large: %d bytes > %d bytes limit", resp.ContentLength, maxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("content too large: %d bytes > %d bytes limit", len(data), maxBytes)
	}

	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	return &remoteContent{
		SourceURL:   rawURL,
		FinalURL:    finalURL,
		Filename:    filenameFromURL(finalURL),
		ContentType: normalizeContentType(resp.Header.Get("Content-Type")),
		Data:        data,
	}, nil
}

func normalizeContentType(raw string) string {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err == nil {
		return strings.ToLower(strings.TrimSpace(mediaType))
	}

	return strings.ToLower(strings.TrimSpace(strings.Split(raw, ";")[0]))
}

func filenameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	base := path.Base(parsed.Path)
	if base == "." || base == "/" {
		return ""
	}

	return base
}

func detectDocumentFormat(filename, contentType string) string {
	contentType = normalizeContentType(contentType)
	if format, ok := supportedDocuments[contentType]; ok {
		return format
	}

	extensions := map[string]string{
		".pdf":  "pdf",
		".docx": "docx",
		".xlsx": "xlsx",
		".pptx": "pptx",
		".txt":  "txt",
		".csv":  "csv",
		".md":   "md",
	}
	lower := strings.ToLower(filename)
	for ext, format := range extensions {
		if strings.HasSuffix(lower, ext) {
			return format
		}
	}

	return ""
}

func extractDocumentText(format string, data []byte) (string, error) {
	switch format {
	case "pdf":
		return extractPDFText(data)
	case "docx":
		return extractDOCXText(data)
	case "xlsx":
		return extractXLSXText(data)
	case "pptx":
		return extractPPTXText(data)
	case "txt":
		return string(data), nil
	case "csv":
		return extractCSVText(data)
	case "md":
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

func extractPDFText(data []byte) (string, error) {
	r := bytes.NewReader(data)
	pdfReader, err := pdf.NewReader(r, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}

	var text strings.Builder
	for pageNum := 1; pageNum <= pdfReader.NumPage(); pageNum++ {
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

func extractDOCXText(data []byte) (string, error) {
	doc, err := docxtotext.OpenReader(bytes.NewReader(data), int64(len(data)))
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

func extractXLSXText(data []byte) (string, error) {
	xlsx, err := xlsxtotext.OpenReader(bytes.NewReader(data), int64(len(data)))
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

func extractPPTXText(data []byte) (string, error) {
	pptx, err := pptxtotext.OpenReader(bytes.NewReader(data), int64(len(data)))
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

func extractCSVText(data []byte) (string, error) {
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

func extractHTMLAsMarkdown(data []byte) (string, string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("failed to parse HTML: %w", err)
	}

	doc.Find("script,style,noscript,svg,canvas").Remove()

	title := normalizeInlineText(doc.Find("title").First().Text())
	body := doc.Find("body")
	if body.Length() == 0 {
		body = doc.Selection
	}

	var markdown strings.Builder
	for _, node := range body.Nodes {
		renderHTMLNodeAsMarkdown(node, &markdown, 0)
	}

	content := strings.TrimSpace(collapseMarkdownSpacing(markdown.String()))
	if content == "" {
		content = normalizeInlineText(doc.Text())
	}

	return title, content, nil
}

func renderHTMLNodeAsMarkdown(node *html.Node, builder *strings.Builder, listDepth int) {
	if node == nil {
		return
	}

	switch node.Type {
	case html.TextNode:
		text := normalizeInlineText(node.Data)
		if text == "" {
			return
		}
		if needsInlineSpace(builder.String()) {
			builder.WriteByte(' ')
		}
		builder.WriteString(text)
	case html.ElementNode:
		switch node.Data {
		case "html", "body", "main", "section", "article", "div":
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				renderHTMLNodeAsMarkdown(child, builder, listDepth)
			}
		case "h1", "h2", "h3", "h4", "h5", "h6":
			ensureMarkdownBlock(builder)
			level := int(node.Data[1] - '0')
			builder.WriteString(strings.Repeat("#", level))
			builder.WriteByte(' ')
			builder.WriteString(normalizeInlineText(extractNodeText(node)))
			builder.WriteString("\n\n")
		case "p":
			text := renderInlineNodeContent(node, listDepth)
			if text == "" {
				return
			}
			ensureMarkdownBlock(builder)
			builder.WriteString(text)
			builder.WriteString("\n\n")
		case "br":
			builder.WriteByte('\n')
		case "ul", "ol":
			ensureMarkdownBlock(builder)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				renderHTMLNodeAsMarkdown(child, builder, listDepth+1)
			}
			builder.WriteByte('\n')
		case "li":
			text := renderInlineNodeContent(node, listDepth)
			if text == "" {
				return
			}
			builder.WriteString(strings.Repeat("  ", max(listDepth-1, 0)))
			builder.WriteString("- ")
			builder.WriteString(text)
			builder.WriteByte('\n')
		case "pre":
			code := strings.TrimSpace(extractNodeText(node))
			if code == "" {
				return
			}
			ensureMarkdownBlock(builder)
			builder.WriteString("```\n")
			builder.WriteString(code)
			builder.WriteString("\n```\n\n")
		case "blockquote":
			text := normalizeInlineText(extractNodeText(node))
			if text == "" {
				return
			}
			ensureMarkdownBlock(builder)
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				builder.WriteString("> ")
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
			builder.WriteByte('\n')
		case "a":
			text := normalizeInlineText(extractNodeText(node))
			href, _ := nodeAttribute(node, "href")
			switch {
			case text == "" && href == "":
				return
			case href == "" || href == text:
				if needsInlineSpace(builder.String()) {
					builder.WriteByte(' ')
				}
				builder.WriteString(text)
			case text == "":
				if needsInlineSpace(builder.String()) {
					builder.WriteByte(' ')
				}
				builder.WriteString(href)
			default:
				if needsInlineSpace(builder.String()) {
					builder.WriteByte(' ')
				}
				builder.WriteString("[")
				builder.WriteString(text)
				builder.WriteString("](")
				builder.WriteString(href)
				builder.WriteByte(')')
			}
		default:
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				renderHTMLNodeAsMarkdown(child, builder, listDepth)
			}
		}
	}
}

func extractNodeText(node *html.Node) string {
	if node == nil {
		return ""
	}

	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current == nil {
			return
		}
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
			text.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)

	return text.String()
}

func nodeAttribute(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return strings.TrimSpace(attr.Val), true
		}
	}
	return "", false
}

func normalizeInlineText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func renderInlineNodeContent(node *html.Node, listDepth int) string {
	var inline strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		renderHTMLNodeAsMarkdown(child, &inline, listDepth)
	}

	return cleanInlineMarkdown(inline.String())
}

func collapseMarkdownSpacing(text string) string {
	lines := strings.Split(text, "\n")
	collapsed := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if strings.TrimSpace(trimmed) == "" {
			if blank {
				continue
			}
			blank = true
			collapsed = append(collapsed, "")
			continue
		}
		blank = false
		collapsed = append(collapsed, trimmed)
	}
	return strings.Join(collapsed, "\n")
}

func ensureMarkdownBlock(builder *strings.Builder) {
	if builder.Len() == 0 {
		return
	}
	current := builder.String()
	if strings.HasSuffix(current, "\n\n") {
		return
	}
	if strings.HasSuffix(current, "\n") {
		builder.WriteByte('\n')
		return
	}
	builder.WriteString("\n\n")
}

func needsInlineSpace(current string) bool {
	if current == "" {
		return false
	}

	last := current[len(current)-1]
	return last != '\n' && last != ' ' && last != '(' && last != '['
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func cleanInlineMarkdown(text string) string {
	text = normalizeInlineText(text)
	replacer := strings.NewReplacer(
		" .", ".",
		" ,", ",",
		" !", "!",
		" ?", "?",
		" ;", ";",
		" :", ":",
		"( ", "(",
		" )", ")",
	)
	return replacer.Replace(text)
}
