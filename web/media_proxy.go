package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	discordRefreshEndpoint = "https://discord.com/api/v9/attachments/refresh-urls"
	mediaProxyTimeout      = 10 * time.Second
	mediaPageMaxBytes      = 512 * 1024
	mediaRefreshMaxBytes   = 64 * 1024
	mediaProxyUserAgent    = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36"
)

var (
	errInvalidMediaURL     = errors.New("invalid media url")
	errUnsupportedMediaURL = errors.New("unsupported media host")
	errMediaProxyNoToken   = errors.New("discord token is not configured")
)

type refreshedMedia struct {
	url     string
	expires time.Time
}

// mediaProxy refreshes expired Discord CDN signatures and resolves GIF page
// links (Tenor, Giphy, Klipy) to a direct image. The admin page uses it as the
// src of every preview image, so the browser never sees an expired CDN link.
type mediaProxy struct {
	client     *http.Client
	token      string
	refreshURL string
	klipyAPI   string

	mu    sync.Mutex
	cache map[string]refreshedMedia
}

func newMediaProxy(token string) *mediaProxy {
	p := &mediaProxy{
		token:      strings.TrimSpace(token),
		refreshURL: discordRefreshEndpoint,
		klipyAPI:   "https://api.klipy.com/api/v1/gifs/",
		cache:      make(map[string]refreshedMedia),
	}
	p.client = &http.Client{
		Timeout:       mediaProxyTimeout,
		CheckRedirect: checkMediaFetchRedirect,
	}
	return p
}

func checkMediaFetchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	if !isAllowedMediaFetchHost(req.URL.Hostname()) {
		return fmt.Errorf("redirect to disallowed host %q", req.URL.Hostname())
	}
	return nil
}

func isAllowedMediaFetchHost(host string) bool {
	switch strings.ToLower(strings.TrimPrefix(host, "www.")) {
	case "tenor.com", "giphy.com", "klipy.com", "api.klipy.com":
		return true
	default:
		return false
	}
}

func isResolvableMediaPage(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isSafeHTTPURL(raw) {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www.")) {
	case "tenor.com", "giphy.com", "klipy.com":
		return true
	default:
		return false
	}
}

func isDiscordAttachmentHost(host string) bool {
	switch strings.ToLower(host) {
	case "cdn.discordapp.com", "media.discordapp.net":
		return true
	default:
		return false
	}
}

func isDiscordAttachmentURL(parsed *url.URL) bool {
	return isDiscordAttachmentHost(parsed.Hostname()) && strings.HasPrefix(parsed.Path, "/attachments/")
}

// mediaThumbSrc builds the admin server URL that serves a preview image.
func mediaThumbSrc(raw string) string {
	return "/data/media/thumb?url=" + url.QueryEscape(strings.TrimSpace(raw))
}

// resolve returns the URL a preview <img> should load, refreshing Discord
// signatures and resolving GIF landing pages when needed.
func (p *mediaProxy) resolve(ctx context.Context, raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isSafeHTTPURL(raw) {
		return "", errInvalidMediaURL
	}

	if isDiscordAttachmentURL(parsed) {
		return p.refreshDiscord(ctx, parsed)
	}

	if directImageURL(raw) != "" {
		return parsed.String(), nil
	}

	switch strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www.")) {
	case "tenor.com", "giphy.com":
		return p.cachedPage(ctx, parsed.String(), func() (string, error) {
			return p.resolveOpenGraphImage(ctx, parsed.String())
		})
	case "klipy.com":
		return p.cachedPage(ctx, parsed.String(), func() (string, error) {
			return p.resolveKlipy(ctx, parsed)
		})
	default:
		return "", errUnsupportedMediaURL
	}
}

// cachedPage remembers resolved GIF page links for an hour; the underlying
// media URLs are stable, and this keeps repeated page views off Tenor/Giphy.
func (p *mediaProxy) cachedPage(ctx context.Context, raw string, resolve func() (string, error)) (string, error) {
	key := "page:" + raw
	if target, ok := p.cached(key); ok {
		return target, nil
	}
	target, err := resolve()
	if err != nil {
		return "", err
	}
	p.store(key, target, time.Now().Add(time.Hour))
	return target, nil
}

func (p *mediaProxy) cached(key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, found := p.cache[key]
	if !found || !entry.expires.After(time.Now().Add(time.Minute)) {
		return "", false
	}
	return entry.url, true
}

func (p *mediaProxy) store(key, target string, expires time.Time) {
	p.mu.Lock()
	p.cache[key] = refreshedMedia{url: target, expires: expires}
	p.mu.Unlock()
}

func (p *mediaProxy) refreshDiscord(ctx context.Context, parsed *url.URL) (string, error) {
	if expires, ok := discordLinkExpiry(parsed); ok && expires.After(time.Now().Add(time.Minute)) {
		return parsed.String(), nil
	}

	cacheKey := "discord:" + parsed.Host + parsed.Path
	if target, ok := p.cached(cacheKey); ok {
		return target, nil
	}

	if p.token == "" {
		return "", errMediaProxyNoToken
	}

	body, err := json.Marshal(map[string][]string{"attachment_urls": {parsed.String()}})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.refreshURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+p.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord refresh returned status %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, mediaRefreshMaxBytes))
	if err != nil {
		return "", err
	}
	var refreshed struct {
		URLs []struct {
			Refreshed string `json:"refreshed"`
		} `json:"refreshed_urls"`
	}
	if err := json.Unmarshal(payload, &refreshed); err != nil {
		return "", err
	}
	if len(refreshed.URLs) == 0 || strings.TrimSpace(refreshed.URLs[0].Refreshed) == "" {
		return "", errors.New("discord refresh returned no url")
	}
	target, err := url.Parse(strings.TrimSpace(refreshed.URLs[0].Refreshed))
	if err != nil || !isDiscordAttachmentURL(target) {
		return "", errors.New("discord refresh returned an unexpected url")
	}
	target.RawQuery = mergeDiscordQuery(target.RawQuery, parsed.RawQuery)

	expires, ok := discordLinkExpiry(target)
	if !ok {
		expires = time.Now().Add(30 * time.Minute)
	}
	p.store(cacheKey, target.String(), expires)
	return target.String(), nil
}

// mergeDiscordQuery keeps non-signature parameters (such as animated=true)
// from the stored URL and takes ex/is/hm from the refreshed URL.
func mergeDiscordQuery(refreshed, original string) string {
	values, err := url.ParseQuery(refreshed)
	if err != nil {
		return refreshed
	}
	extra, err := url.ParseQuery(original)
	if err != nil {
		return refreshed
	}
	for key, list := range extra {
		switch key {
		case "ex", "is", "hm":
			continue
		}
		for _, value := range list {
			values.Add(key, value)
		}
	}
	return values.Encode()
}

func discordLinkExpiry(parsed *url.URL) (time.Time, bool) {
	raw := parsed.Query().Get("ex")
	if raw == "" {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(raw, 16, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

func (p *mediaProxy) resolveOpenGraphImage(ctx context.Context, raw string) (string, error) {
	document, err := p.fetchPage(ctx, raw)
	if err != nil {
		return "", err
	}
	image := openGraphImage(document)
	if image == "" {
		return "", errors.New("no preview image on the page")
	}
	return image, nil
}

func (p *mediaProxy) fetchPage(ctx context.Context, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", mediaProxyUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("page returned status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, mediaPageMaxBytes))
}

// openGraphImage returns the best direct image advertised by a page's meta
// tags, preferring animated GIFs over static preview frames.
func openGraphImage(document []byte) string {
	tokenizer := html.NewTokenizer(bytes.NewReader(document))
	var first string
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return first
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}
		name, hasAttrs := tokenizer.TagName()
		if string(name) != "meta" || !hasAttrs {
			continue
		}
		var property, content string
		for {
			key, value, more := tokenizer.TagAttr()
			switch strings.ToLower(string(key)) {
			case "property", "name":
				property = strings.ToLower(string(value))
			case "content":
				content = html.UnescapeString(string(value))
			}
			if !more {
				break
			}
		}
		if property != "og:image" && property != "twitter:image" {
			continue
		}
		content = strings.TrimSpace(content)
		if !isPublicHTTPURL(content) {
			continue
		}
		if strings.EqualFold(extensionOf(content), ".gif") {
			return content
		}
		if first == "" {
			first = content
		}
	}
}

func (p *mediaProxy) resolveKlipy(ctx context.Context, parsed *url.URL) (string, error) {
	slug := strings.Trim(strings.TrimPrefix(parsed.Path, "/gifs/"), "/")
	if slug == "" || strings.Contains(slug, "/") {
		return "", errInvalidMediaURL
	}
	endpoint := p.klipyAPI + url.PathEscape(slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("klipy returned status %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			File map[string]map[string]struct {
				URL string `json:"url"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, mediaPageMaxBytes)).Decode(&payload); err != nil {
		return "", err
	}
	for _, size := range []string{"md", "hd", "sm", "xs"} {
		if media, ok := payload.Data.File[size]; ok {
			if image, ok := media["gif"]; ok && isPublicHTTPURL(image.URL) {
				return image.URL, nil
			}
		}
	}
	return "", errors.New("klipy returned no gif")
}

func extensionOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	path := parsed.Path
	if index := strings.LastIndex(path, "."); index >= 0 {
		return path[index:]
	}
	return ""
}

func isPublicHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isSafeHTTPURL(raw) {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return false
	}
	return true
}

func (s *Server) handleMediaThumb(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mediaProxyTimeout+time.Second)
	defer cancel()

	target, err := s.mediaProxy.resolve(ctx, raw)
	if err != nil {
		http.Error(w, "preview unavailable", http.StatusBadGateway)
		return
	}
	// Let the browser reuse a preview for the current page view, but always
	// come back to this endpoint so an expired CDN signature gets refreshed.
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.Redirect(w, r, target, http.StatusFound)
}
