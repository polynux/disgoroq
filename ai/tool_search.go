package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

type WebSearchTool struct {
	config  SearchToolConfig
	baseURL *url.URL
	client  *http.Client
}

type searxngSearchResponse struct {
	Query   string                `json:"query"`
	Results []searxngSearchResult `json:"results"`
}

type searxngSearchResult struct {
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Engine   string   `json:"engine"`
	Engines  []string `json:"engines"`
	Category string   `json:"category"`
}

func NewWebSearchTool(cfg SearchToolConfig) *WebSearchTool {
	parsedURL, _ := url.Parse(cfg.BaseURL)

	return &WebSearchTool{
		config:  cfg,
		baseURL: parsedURL,
		client:  &http.Client{},
	}
}

func (t *WebSearchTool) Definition() ToolDefinition {
	noAdditionalProperties := false

	return ToolDefinition{
		Function: ToolFunctionDefinition{
			Name:        defaultWebSearchToolName,
			Description: "Search the web with the configured SearXNG instance and return compact result snippets with URLs for later fetching.",
			Parameters: ToolSchema{
				Type: "object",
				Properties: map[string]ToolProperty{
					"query": {
						Type:        "string",
						Description: "The search query to run.",
					},
					"max_results": {
						Type:        "integer",
						Description: "Optional maximum number of results to return.",
					},
					"language": {
						Type:        "string",
						Description: "Optional result language code supported by the search backend, for example fr or en.",
					},
				},
				Required:             []string{"query"},
				AdditionalProperties: &noAdditionalProperties,
			},
		},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		Language   string `json:"language"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  fmt.Sprintf("invalid arguments: %v", err),
			IsError:  true,
		}, fmt.Errorf("decode web search arguments: %w", err)
	}

	query := normalizeSearchText(args.Query)
	if query == "" {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  "missing required query argument",
			IsError:  true,
		}, fmt.Errorf("missing query argument")
	}

	if t.baseURL == nil {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  "invalid search backend configuration",
			IsError:  true,
		}, fmt.Errorf("invalid search backend configuration")
	}

	searchURL := t.baseURL.JoinPath("search")
	params := searchURL.Query()
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "general")
	params.Set("safesearch", strconv.Itoa(t.config.SafeSearch))

	language := strings.TrimSpace(args.Language)
	if language == "" {
		language = strings.TrimSpace(t.config.DefaultLanguage)
	}
	if language != "" {
		params.Set("language", language)
	}
	searchURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  fmt.Sprintf("failed to create search request: %v", err),
			IsError:  true,
		}, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if t.config.UserAgent != "" {
		req.Header.Set("User-Agent", t.config.UserAgent)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  fmt.Sprintf("search request failed: %v", err),
			IsError:  true,
		}, fmt.Errorf("execute search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  fmt.Sprintf("search backend returned %s", resp.Status),
			IsError:  true,
		}, fmt.Errorf("search backend returned %s", resp.Status)
	}

	var payload searxngSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return &ToolResult{
			ToolName: defaultWebSearchToolName,
			Content:  fmt.Sprintf("failed to decode search response: %v", err),
			IsError:  true,
		}, fmt.Errorf("decode search response: %w", err)
	}

	rendered, truncated := t.renderResults(query, payload.Results, requestedMaxResults(args.MaxResults, t.config.MaxResults))
	logger.Debug("Web search completed",
		zap.String("provider", t.config.Provider),
		zap.String("query", query),
		zap.Int("results_returned", minInt(len(payload.Results), t.config.MaxResults)),
		zap.Bool("truncated", truncated))

	return &ToolResult{
		ToolName: defaultWebSearchToolName,
		Content:  rendered,
	}, nil
}

func (t *WebSearchTool) renderResults(query string, results []searxngSearchResult, limit int) (string, bool) {
	var builder strings.Builder
	builder.WriteString("## Web search results\n")
	builder.WriteString("- Query: ")
	builder.WriteString(query)

	if len(results) == 0 {
		builder.WriteString("\n- Results: 0\n\nNo results found.")
		return builder.String(), false
	}

	if limit < len(results) {
		results = results[:limit]
	}
	builder.WriteString("\n- Results: ")
	builder.WriteString(strconv.Itoa(len(results)))
	builder.WriteString("\n\n")

	for idx, result := range results {
		title := normalizeSearchText(result.Title)
		if title == "" {
			title = result.URL
		}
		builder.WriteString(strconv.Itoa(idx + 1))
		builder.WriteString(". [")
		builder.WriteString(title)
		builder.WriteString("](")
		builder.WriteString(strings.TrimSpace(result.URL))
		builder.WriteString(")\n")

		snippet := normalizeSearchText(result.Content)
		if snippet != "" {
			builder.WriteString("   - Snippet: ")
			builder.WriteString(snippet)
			builder.WriteString("\n")
		}

		engines := result.Engines
		if len(engines) == 0 && result.Engine != "" {
			engines = []string{result.Engine}
		}
		if len(engines) > 0 {
			builder.WriteString("   - Engines: ")
			builder.WriteString(strings.Join(uniqueStrings(engines), ", "))
			builder.WriteString("\n")
		}

		if category := normalizeSearchText(result.Category); category != "" {
			builder.WriteString("   - Category: ")
			builder.WriteString(category)
			builder.WriteString("\n")
		}
	}

	rendered := strings.TrimSpace(builder.String())
	return truncateToolContent(rendered, t.config.MaxCharacters)
}

func requestedMaxResults(requested, configured int) int {
	if configured < 1 {
		configured = 5
	}
	if requested < 1 {
		return configured
	}
	if requested > configured {
		return configured
	}
	return requested
}

func truncateToolContent(content string, maxCharacters int) (string, bool) {
	if maxCharacters > 0 && len(content) > maxCharacters {
		return strings.TrimSpace(content[:maxCharacters]) + "\n\n...[truncated]", true
	}
	return content, false
}

func normalizeSearchText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeSearchText(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
