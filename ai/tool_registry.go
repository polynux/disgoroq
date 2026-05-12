package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

const (
	defaultWebToolName       = "web_fetch"
	defaultWebSearchToolName = "web_search"
)

type ToolRuntimeConfig struct {
	Enabled          bool
	MaxRounds        int
	MaxCallsPerRound int
	MaxCallsTotal    int
	Timeout          time.Duration
	Web              WebToolConfig
	Search           SearchToolConfig
}

type WebToolConfig struct {
	Enabled              bool
	AllowedSchemes       []string
	UserAgent            string
	MaxBytes             int64
	MaxCharacters        int
	AllowPrivateNetworks bool
}

type SearchToolConfig struct {
	Enabled         bool
	Provider        string
	BaseURL         string
	UserAgent       string
	MaxResults      int
	MaxCharacters   int
	DefaultLanguage string
	SafeSearch      int
}

type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
}

type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
}

func (r ToolResult) Message() Message {
	content := r.Content
	if r.IsError && !strings.HasPrefix(content, "Tool error:") {
		content = "Tool error: " + content
	}
	return ToolResultMessage(r.ToolCallID, r.ToolName, content)
}

type ToolRegistry struct {
	config ToolRuntimeConfig
	order  []string
	tools  map[string]Tool
}

func (c ToolRuntimeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.MaxRounds < 1 {
		return fmt.Errorf("max rounds must be at least 1")
	}
	if c.MaxCallsPerRound < 1 {
		return fmt.Errorf("max calls per round must be at least 1")
	}
	if c.MaxCallsTotal < c.MaxCallsPerRound {
		return fmt.Errorf("max calls total must be greater than or equal to max calls per round")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if err := c.Web.Validate(); err != nil {
		return err
	}

	return c.Search.Validate()
}

func (c WebToolConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if len(c.AllowedSchemes) == 0 {
		return fmt.Errorf("allowed schemes cannot be empty")
	}
	for _, scheme := range c.AllowedSchemes {
		switch strings.ToLower(strings.TrimSpace(scheme)) {
		case "http", "https":
		default:
			return fmt.Errorf("unsupported allowed scheme %q", scheme)
		}
	}
	if c.UserAgent == "" {
		return errors.New("user agent cannot be empty")
	}
	if c.MaxBytes < 1 {
		return errors.New("max bytes must be positive")
	}
	if c.MaxCharacters < 1 {
		return errors.New("max characters must be positive")
	}

	return nil
}

func (c SearchToolConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.Provider == "" {
		return errors.New("search provider cannot be empty")
	}
	if !strings.EqualFold(strings.TrimSpace(c.Provider), "searxng") {
		return fmt.Errorf("unsupported search provider %q", c.Provider)
	}
	if c.BaseURL == "" {
		return errors.New("search base URL cannot be empty")
	}
	parsedURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid search base URL: %w", err)
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("invalid search base URL %q", c.BaseURL)
	}
	if c.UserAgent == "" {
		return errors.New("search user agent cannot be empty")
	}
	if c.MaxResults < 1 {
		return errors.New("search max results must be positive")
	}
	if c.MaxCharacters < 1 {
		return errors.New("search max characters must be positive")
	}
	if c.SafeSearch < 0 || c.SafeSearch > 2 {
		return fmt.Errorf("search safe search must be between 0 and 2, got %d", c.SafeSearch)
	}

	return nil
}

func NewToolRegistry(cfg ToolRuntimeConfig) *ToolRegistry {
	registry := &ToolRegistry{
		config: cfg,
		tools:  make(map[string]Tool),
	}
	if !cfg.Enabled {
		return registry
	}

	if cfg.Web.Enabled {
		registry.Register(NewWebFetchTool(cfg.Web))
	}
	if cfg.Search.Enabled {
		registry.Register(NewWebSearchTool(cfg.Search))
	}

	return registry
}

func (r *ToolRegistry) Register(tool Tool) {
	if r == nil || tool == nil {
		return
	}

	definition := tool.Definition()
	name := definition.Function.Name
	if name == "" {
		return
	}
	if _, exists := r.tools[name]; exists {
		r.tools[name] = tool
		return
	}

	r.tools[name] = tool
	r.order = append(r.order, name)
}

func (r *ToolRegistry) Enabled() bool {
	return r != nil && r.config.Enabled && len(r.tools) > 0
}

func (r *ToolRegistry) Definitions() []ToolDefinition {
	if !r.Enabled() {
		return nil
	}

	definitions := make([]ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		definitions = append(definitions, r.tools[name].Definition())
	}
	return definitions
}

func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	if !r.Enabled() {
		return &ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Content:    "tool calling is disabled",
			IsError:    true,
		}, errors.New("tool calling is disabled")
	}

	toolName := strings.TrimSpace(call.Function.Name)
	if toolName == "" {
		return &ToolResult{
			ToolCallID: call.ID,
			Content:    "missing tool name",
			IsError:    true,
		}, errors.New("missing tool name")
	}

	tool, ok := r.tools[toolName]
	if !ok {
		return &ToolResult{
			ToolCallID: call.ID,
			ToolName:   toolName,
			Content:    fmt.Sprintf("unsupported tool %q", toolName),
			IsError:    true,
		}, fmt.Errorf("unsupported tool %q", toolName)
	}

	execCtx := ctx
	cancel := func() {}
	if r.config.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, r.config.Timeout)
	}
	defer cancel()

	result, err := tool.Execute(execCtx, call)
	if result != nil {
		result.ToolCallID = call.ID
		if result.ToolName == "" {
			result.ToolName = toolName
		}
	}
	if err != nil {
		logger.Warn("Tool execution failed",
			zap.String("tool", toolName),
			zap.String("tool_call_id", call.ID),
			zap.Error(err))
	}

	return result, err
}

type WebFetchTool struct {
	config WebToolConfig
	client *http.Client
}

func NewWebFetchTool(cfg WebToolConfig) *WebFetchTool {
	return &WebFetchTool{
		config: cfg,
		client: &http.Client{
			Transport: newWebFetchTransport(cfg.AllowPrivateNetworks),
			Timeout:   0,
		},
	}
}

func (t *WebFetchTool) Definition() ToolDefinition {
	noAdditionalProperties := false

	return ToolDefinition{
		Function: ToolFunctionDefinition{
			Name:        defaultWebToolName,
			Description: "Fetch a web page or document from the public internet and return a markdown-friendly summary of its contents.",
			Parameters: ToolSchema{
				Type: "object",
				Properties: map[string]ToolProperty{
					"url": {
						Type:        "string",
						Description: "The http or https URL to fetch.",
					},
				},
				Required:             []string{"url"},
				AdditionalProperties: &noAdditionalProperties,
			},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  fmt.Sprintf("invalid arguments: %v", err),
			IsError:  true,
		}, fmt.Errorf("decode web tool arguments: %w", err)
	}

	targetURL := strings.TrimSpace(args.URL)
	if targetURL == "" {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  "missing required url argument",
			IsError:  true,
		}, errors.New("missing url argument")
	}

	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  fmt.Sprintf("invalid url: %v", err),
			IsError:  true,
		}, fmt.Errorf("parse web tool url: %w", err)
	}

	if !t.isAllowedScheme(parsedURL.Scheme) {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  fmt.Sprintf("unsupported URL scheme %q", parsedURL.Scheme),
			IsError:  true,
		}, fmt.Errorf("unsupported URL scheme %q", parsedURL.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  fmt.Sprintf("failed to create request: %v", err),
			IsError:  true,
		}, fmt.Errorf("build web tool request: %w", err)
	}
	if t.config.UserAgent != "" {
		req.Header.Set("User-Agent", t.config.UserAgent)
	}

	content, err := downloadRemoteContent(req.Context(), withRequestHeaders(t.client, req.Header), targetURL, t.config.MaxBytes)
	if err != nil {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  err.Error(),
			IsError:  true,
		}, err
	}

	markdown, truncated, err := t.renderContent(content)
	if err != nil {
		return &ToolResult{
			ToolName: defaultWebToolName,
			Content:  err.Error(),
			IsError:  true,
		}, err
	}

	var builder strings.Builder
	builder.WriteString("## Web fetch result\n")
	builder.WriteString("- Source URL: ")
	builder.WriteString(content.SourceURL)
	builder.WriteString("\n- Final URL: ")
	builder.WriteString(content.FinalURL)
	builder.WriteString("\n- Content-Type: ")
	builder.WriteString(content.ContentType)
	if content.Filename != "" {
		builder.WriteString("\n- Filename: ")
		builder.WriteString(content.Filename)
	}
	if truncated {
		builder.WriteString("\n- Truncated: yes")
	}
	builder.WriteString("\n\n")
	builder.WriteString(markdown)

	logger.Debug("Web tool fetched content",
		zap.String("source_url", content.SourceURL),
		zap.String("final_url", content.FinalURL),
		zap.String("content_type", content.ContentType),
		zap.Int("bytes", len(content.Data)),
		zap.Bool("truncated", truncated))

	return &ToolResult{
		ToolName: defaultWebToolName,
		Content:  builder.String(),
	}, nil
}

func (t *WebFetchTool) renderContent(content *remoteContent) (string, bool, error) {
	var builder strings.Builder
	contentType := content.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	switch {
	case contentType == "text/html" || contentType == "application/xhtml+xml":
		title, body, err := extractHTMLAsMarkdown(content.Data)
		if err != nil {
			return "", false, err
		}
		if title != "" {
			builder.WriteString("# ")
			builder.WriteString(title)
			builder.WriteString("\n\n")
		}
		builder.WriteString(body)
	default:
		if format := detectDocumentFormat(content.Filename, contentType); format != "" {
			text, err := extractDocumentText(format, content.Data)
			if err != nil {
				return "", false, err
			}
			if format == "md" {
				builder.WriteString(text)
			} else {
				builder.WriteString("## Extracted content\n\n")
				builder.WriteString(text)
			}
		} else if strings.HasPrefix(contentType, "text/") {
			builder.WriteString("## Extracted content\n\n")
			builder.WriteString(string(content.Data))
		} else {
			return "", false, fmt.Errorf("unsupported content type %q", contentType)
		}
	}

	rendered := strings.TrimSpace(builder.String())
	if rendered == "" {
		return "", false, errors.New("fetched content was empty after extraction")
	}

	if t.config.MaxCharacters > 0 && len(rendered) > t.config.MaxCharacters {
		return strings.TrimSpace(rendered[:t.config.MaxCharacters]) + "\n\n...[truncated]", true, nil
	}

	return rendered, false, nil
}

func (t *WebFetchTool) isAllowedScheme(scheme string) bool {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	for _, allowed := range t.config.AllowedSchemes {
		if scheme == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func withRequestHeaders(client *http.Client, headers http.Header) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	if len(headers) == 0 {
		return client
	}

	clone := *client
	baseTransport, _ := client.Transport.(*http.Transport)
	if baseTransport != nil {
		transportClone := baseTransport.Clone()
		transportClone.ProxyConnectHeader = headers.Clone()
		clone.Transport = roundTripperWithHeaders{
			base:    transportClone,
			headers: headers.Clone(),
		}
		return &clone
	}

	clone.Transport = roundTripperWithHeaders{
		base:    client.Transport,
		headers: headers.Clone(),
	}
	return &clone
}

type roundTripperWithHeaders struct {
	base    http.RoundTripper
	headers http.Header
}

func (r roundTripperWithHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	for key, values := range r.headers {
		cloned.Header.Del(key)
		for _, value := range values {
			cloned.Header.Add(key, value)
		}
	}
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func newWebFetchTransport(allowPrivate bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if allowPrivate {
		return transport
	}

	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}

		if blockedHostName(host) {
			return nil, fmt.Errorf("blocked private host %q", host)
		}

		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err == nil {
			for _, ip := range ips {
				if isPrivateIP(ip) {
					return nil, fmt.Errorf("blocked private address %s", ip.String())
				}
			}
		}

		return dialer.DialContext(ctx, network, address)
	}

	return transport
}

func blockedHostName(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || strings.HasSuffix(host, ".local")
}

func isPrivateIP(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified()
}
