package medialibrary

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/disgoorg/disgo/rest"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/logger"
)

const (
	defaultLinkCheckTimeout = 1500 * time.Millisecond
	maxValidationCandidates = 3
	linkCheckUserAgent      = "DisgoroQ/1.0 (+https://github.com/polynux/disgoroq)"
	deadLinkDeleteTimeout   = 1 * time.Second
)

// DiscordRestClient is the minimal subset of disgo's rest.Client needed to call
// Discord's attachment refresh endpoint with the bot's configured token and User-Agent.
type DiscordRestClient interface {
	Do(endpoint *rest.CompiledEndpoint, rqBody any, rsBody any, opts ...rest.RequestOpt) error
}

var (
	refreshAttachmentURLsEndpoint = rest.NewEndpoint(http.MethodPost, "/attachments/refresh-urls")
	defaultValidationHTTPClient   = &http.Client{}
)

type refreshAttachmentURLsRequest struct {
	AttachmentURLs []string `json:"attachment_urls"`
}

type refreshedAttachmentURL struct {
	Original  string `json:"original"`
	Refreshed string `json:"refreshed"`
}

type refreshAttachmentURLsResponse struct {
	RefreshedURLs []refreshedAttachmentURL `json:"refreshed_urls"`
}

type refreshOutcome int

const (
	refreshStatusOK refreshOutcome = iota
	refreshStatusNotFound
	refreshStatusError
)

type linkCheckOutcome int

const (
	linkAlive linkCheckOutcome = iota
	linkDead
	linkUnknown
)

// SetDiscordRestClient configures the Discord REST client used to refresh
// Discord CDN attachment URLs via POST /attachments/refresh-urls.
func (s *Service) SetDiscordRestClient(client DiscordRestClient) {
	if s == nil {
		return
	}
	s.clientMu.Lock()
	s.restClient = client
	s.clientMu.Unlock()
}

// SetHTTPClient overrides the HTTP client used for link validation checks.
// Passing nil restores the default client.
func (s *Service) SetHTTPClient(client *http.Client) {
	if s == nil {
		return
	}
	s.clientMu.Lock()
	s.httpClient = client
	s.clientMu.Unlock()
}

func (s *Service) discordRestClient() DiscordRestClient {
	s.clientMu.RLock()
	client := s.restClient
	s.clientMu.RUnlock()
	return client
}

func (s *Service) validationHTTPClient() *http.Client {
	s.clientMu.RLock()
	client := s.httpClient
	s.clientMu.RUnlock()
	if client != nil {
		return client
	}
	return defaultValidationHTTPClient
}

func (s *Service) linkCheckTimeout() time.Duration {
	s.clientMu.RLock()
	timeout := s.checkTimeout
	s.clientMu.RUnlock()
	if timeout > 0 {
		return timeout
	}
	return defaultLinkCheckTimeout
}

func (s *Service) validationExhausted(attempts int) bool {
	return s.cfg.ValidateLinks && attempts >= maxValidationCandidates
}

// prepareCandidate refreshes Discord CDN URLs (when a Discord REST client is
// configured) and, when validate_links is enabled, checks liveness via HEAD
// (with GET Range fallback on 405/501). Confirmed dead rows are deleted from DB
// and reported with dead=true so callers can try the next candidate.
func (s *Service) prepareCandidate(ctx context.Context, entry Entry) (postedURL string, dead bool) {
	isCDN := ai.IsDiscordCDNURL(entry.URL)
	if !s.cfg.ValidateLinks && !isCDN {
		return entry.URL, false
	}

	restClient := s.discordRestClient()
	if !s.cfg.ValidateLinks && isCDN && restClient == nil {
		return entry.URL, false
	}

	checkCtx, cancel := context.WithTimeout(ctx, s.linkCheckTimeout())
	defer cancel()

	targetURL := entry.URL
	if isCDN {
		if restClient == nil {
			logger.Debug("Media library Discord CDN refresh unavailable; posting URL without validation",
				zap.String("url", entry.URL),
				zap.String("guild_id", entry.GuildID))
			return entry.URL, false
		}
		refreshed, outcome, err := refreshDiscordCDNURL(checkCtx, restClient, entry.URL)
		switch outcome {
		case refreshStatusOK:
			targetURL = refreshed
		case refreshStatusNotFound:
			if !s.cfg.ValidateLinks {
				logger.Debug("Media library Discord CDN refresh returned no URL; posting canonical URL",
					zap.String("url", entry.URL),
					zap.String("guild_id", entry.GuildID))
				return entry.URL, false
			}
			s.deleteDeadEntry(ctx, entry, 0, "refresh_not_found")
			return "", true
		default:
			logger.Debug("Media library Discord CDN refresh failed; posting URL anyway",
				zap.String("url", entry.URL),
				zap.String("guild_id", entry.GuildID),
				zap.Error(err))
			return entry.URL, false
		}
	}

	if !s.cfg.ValidateLinks {
		return targetURL, false
	}

	outcome, status, err := checkLink(checkCtx, s.validationHTTPClient(), targetURL)
	switch outcome {
	case linkAlive:
		return targetURL, false
	case linkDead:
		s.deleteDeadEntry(ctx, entry, status, "")
		return "", true
	default:
		fields := []zap.Field{
			zap.String("url", entry.URL),
			zap.String("guild_id", entry.GuildID),
		}
		if status != 0 {
			fields = append(fields, zap.Int("status", status))
		}
		if err != nil {
			fields = append(fields, zap.Error(err))
		}
		logger.Debug("Media library link validation inconclusive; posting URL anyway", fields...)
		return targetURL, false
	}
}

func refreshDiscordCDNURL(ctx context.Context, restClient DiscordRestClient, rawURL string) (string, refreshOutcome, error) {
	var resp refreshAttachmentURLsResponse
	err := restClient.Do(
		refreshAttachmentURLsEndpoint.Compile(nil),
		refreshAttachmentURLsRequest{AttachmentURLs: []string{rawURL}},
		&resp,
		rest.WithCtx(ctx),
	)
	if err != nil {
		return "", refreshStatusError, err
	}

	canonicalTarget := ai.CanonicalizeMediaURL(rawURL)
	for _, item := range resp.RefreshedURLs {
		refreshed := strings.TrimSpace(item.Refreshed)
		if refreshed == "" {
			continue
		}
		if item.Original == rawURL || ai.CanonicalizeMediaURL(item.Original) == canonicalTarget {
			return refreshed, refreshStatusOK, nil
		}
	}
	if len(resp.RefreshedURLs) == 1 && resp.RefreshedURLs[0].Original == "" {
		if refreshed := strings.TrimSpace(resp.RefreshedURLs[0].Refreshed); refreshed != "" {
			return refreshed, refreshStatusOK, nil
		}
	}
	return "", refreshStatusNotFound, nil
}

func checkLink(ctx context.Context, client *http.Client, targetURL string) (linkCheckOutcome, int, error) {
	status, err := doLinkCheckRequest(ctx, client, http.MethodHead, targetURL, false)
	if err != nil {
		return linkUnknown, 0, err
	}
	if status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented {
		status, err = doLinkCheckRequest(ctx, client, http.MethodGet, targetURL, true)
		if err != nil {
			return linkUnknown, 0, err
		}
	}
	return classifyLinkStatus(status), status, nil
}

func doLinkCheckRequest(ctx context.Context, client *http.Client, method, targetURL string, rangeFirstByte bool) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", linkCheckUserAgent)
	if rangeFirstByte {
		req.Header.Set("Range", "bytes=0-0")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	if resp.Body != nil {
		_, _ = io.CopyN(io.Discard, resp.Body, 512)
		_ = resp.Body.Close()
	}
	return resp.StatusCode, nil
}

func classifyLinkStatus(status int) linkCheckOutcome {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return linkDead
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		return linkAlive
	default:
		return linkUnknown
	}
}

func (s *Service) deleteDeadEntry(ctx context.Context, entry Entry, status int, reason string) {
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	delCtx, cancel := context.WithTimeout(baseCtx, deadLinkDeleteTimeout)
	defer cancel()

	if err := s.repo.DeleteEntry(delCtx, entry.URL, entry.GuildID); err != nil {
		logger.Warn("Media library failed to delete dead link",
			zap.String("url", entry.URL),
			zap.String("guild_id", entry.GuildID),
			zap.Int("status", status),
			zap.Error(err))
		return
	}

	fields := []zap.Field{
		zap.String("url", entry.URL),
		zap.String("guild_id", entry.GuildID),
		zap.Int("status", status),
	}
	if reason != "" {
		fields = append(fields, zap.String("reason", reason))
	}
	logger.Info("media library dead link removed", fields...)
}
