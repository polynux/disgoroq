package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/html"

	"polynux/disgoroq/logger"
)

const (
	linkPreviewTimeout  = 8 * time.Second
	linkPreviewMaxBytes = 512 * 1024
	linkPreviewDescMax  = 180
	linkPreviewCacheTTL = time.Hour
)

type linkPreview struct {
	URL         string
	Title       string
	Description string
	Image       string
	Site        string
}

type linkPreviewEntry struct {
	preview linkPreview
	expires time.Time
}

// linkPreviewer fetches OpenGraph metadata for link entries in the media
// library. Its HTTP client refuses private addresses, so a stored link cannot
// turn the admin server into an SSRF proxy.
type linkPreviewer struct {
	client *http.Client

	mu    sync.Mutex
	cache map[string]linkPreviewEntry
}

func newLinkPreviewer() *linkPreviewer {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !isPublicIP(ip) {
				return errors.New("blocked non-public address")
			}
			return nil
		},
	}
	return &linkPreviewer{
		client: &http.Client{
			Timeout: linkPreviewTimeout,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 6 * time.Second,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 4 {
					return errors.New("too many redirects")
				}
				if !isSafeHTTPURL(req.URL.String()) || req.URL.Hostname() == "" {
					return errors.New("invalid redirect")
				}
				return nil
			},
		},
		cache: make(map[string]linkPreviewEntry),
	}
}

func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return !cgnat.Contains(ip)
}

func (l *linkPreviewer) resolve(ctx context.Context, raw string) (linkPreview, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isSafeHTTPURL(raw) {
		return linkPreview{}, errInvalidMediaURL
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	if host == "" || host == "localhost" {
		return linkPreview{}, errUnsupportedMediaURL
	}

	l.mu.Lock()
	entry, found := l.cache[raw]
	l.mu.Unlock()
	if found && entry.expires.After(time.Now()) {
		return entry.preview, nil
	}

	preview, err := l.fetch(ctx, parsed)
	if err != nil {
		return linkPreview{}, err
	}
	l.mu.Lock()
	l.cache[raw] = linkPreviewEntry{preview: preview, expires: time.Now().Add(linkPreviewCacheTTL)}
	l.mu.Unlock()
	return preview, nil
}

func (l *linkPreviewer) fetch(ctx context.Context, parsed *url.URL) (linkPreview, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return linkPreview{}, err
	}
	req.Header.Set("User-Agent", mediaProxyUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := l.client.Do(req)
	if err != nil {
		return linkPreview{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return linkPreview{}, fmt.Errorf("link returned status %d", resp.StatusCode)
	}
	document, err := io.ReadAll(io.LimitReader(resp.Body, linkPreviewMaxBytes))
	if err != nil {
		return linkPreview{}, err
	}
	preview := parseLinkPreview(document, resp.Request.URL)
	preview.URL = parsed.String()
	if preview.Site == "" {
		preview.Site = strings.TrimPrefix(parsed.Hostname(), "www.")
	}
	return preview, nil
}

// parseLinkPreview pulls the OpenGraph/Twitter card fields that most sites
// already serve to bots and link unfurlers.
func parseLinkPreview(document []byte, base *url.URL) linkPreview {
	tokenizer := html.NewTokenizer(bytes.NewReader(document))
	var (
		preview  linkPreview
		images   []string
		titleTag string
		inTitle  bool
	)
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case html.ErrorToken:
			preview.Title = strings.TrimSpace(html.UnescapeString(preview.Title))
			if preview.Title == "" {
				preview.Title = strings.TrimSpace(html.UnescapeString(titleTag))
			}
			preview.Description = truncateDisplayRunes(strings.TrimSpace(preview.Description), linkPreviewDescMax)
			preview.Image = pickLinkImage(images, base)
			return preview
		case html.StartTagToken:
			name, _ := tokenizer.TagName()
			switch string(name) {
			case "title":
				inTitle = true
			case "meta":
				property, content := metaTagAttributes(tokenizer)
				switch property {
				case "og:title", "twitter:title":
					if preview.Title == "" {
						preview.Title = content
					}
				case "og:description", "twitter:description":
					if preview.Description == "" {
						preview.Description = content
					}
				case "og:site_name":
					if preview.Site == "" {
						preview.Site = content
					}
				case "og:image", "twitter:image":
					if image := resolveLinkImage(content, base); image != "" {
						images = append(images, image)
					}
				}
			}
		case html.TextToken:
			if inTitle && len(titleTag) < 512 {
				titleTag += string(tokenizer.Text())
			}
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			if string(name) == "title" {
				inTitle = false
			}
		}
	}
}

// metaTagAttributes reads the attributes already positioned by TagName.
func metaTagAttributes(tokenizer *html.Tokenizer) (property, content string) {
	for {
		key, value, more := tokenizer.TagAttr()
		switch strings.ToLower(string(key)) {
		case "property", "name":
			property = strings.ToLower(strings.TrimSpace(string(value)))
		case "content":
			content = html.UnescapeString(string(value))
		}
		if !more {
			return property, content
		}
	}
}

func resolveLinkImage(raw string, base *url.URL) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if !isPublicHTTPURL(parsed.String()) {
		return ""
	}
	return parsed.String()
}

func pickLinkImage(images []string, base *url.URL) string {
	if len(images) == 0 {
		return ""
	}
	for _, image := range images {
		if strings.EqualFold(extensionOf(image), ".gif") {
			return image
		}
	}
	for _, image := range images {
		if directImageURL(image) != "" {
			return image
		}
	}
	return images[0]
}

type embedView struct {
	URL         string
	Title       string
	Description string
	Image       string
	Site        string
	ShortURL    string
}

func (s *Server) handleMediaEmbed(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	view := embedView{
		URL:      raw,
		ShortURL: shortDisplayURL(raw),
	}
	if parsed, err := url.Parse(raw); err == nil {
		view.Site = strings.TrimPrefix(parsed.Hostname(), "www.")
	}

	ctx, cancel := context.WithTimeout(r.Context(), linkPreviewTimeout+time.Second)
	defer cancel()
	preview, err := s.linkPreviewer.resolve(ctx, raw)
	if err != nil {
		if host := hostOf(raw); host != "" {
			logger.Warn("Link preview failed", zap.String("host", host), zap.Error(err))
		}
	} else {
		view.Title = preview.Title
		view.Description = preview.Description
		view.Site = preview.Site
		if preview.Image != "" {
			view.Image = embedImageSrc(preview.Image)
		}
	}
	if view.Title == "" {
		view.Title = view.ShortURL
	}
	s.renderFragment(w, "data_embed", http.StatusOK, view)
}

// embedImageSrc refreshes Discord-hosted card images and leaves other public
// images to the browser.
func embedImageSrc(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && isDiscordAttachmentURL(parsed) {
		return mediaThumbSrc(raw)
	}
	return raw
}

func hostOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
