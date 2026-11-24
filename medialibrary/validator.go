package medialibrary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/rest"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

const (
	defaultLinkCheckTimeout = 1500 * time.Millisecond
	maxValidationCandidates = 3
	maxLinkCheckRedirects   = 5
	linkCheckUserAgent      = "DisgoroQ/1.0 (+https://github.com/polynux/disgoroq)"
	deadLinkDeleteTimeout   = 2 * time.Second
)

// DiscordRestClient is the minimal subset of disgo's rest.Client needed to call
// Discord's attachment refresh endpoint with the bot's configured token and User-Agent.
type DiscordRestClient interface {
	Do(endpoint *rest.CompiledEndpoint, rqBody any, rsBody any, opts ...rest.RequestOpt) error
}

var (
	refreshAttachmentURLsEndpoint = rest.NewEndpoint(http.MethodPost, "/attachments/refresh-urls")
	defaultValidationHTTPClient   = newValidationHTTPClient()
	errLinkBlocked                = errors.New("link check blocked by SSRF guard")
	urlInErrorPattern             = regexp.MustCompile(`https?://[^\s"'<>]+`)

	// IPv4 ranges not covered by netip.Addr.IsPrivate / IsLoopback / IsLinkLocal*:
	// "this network", carrier-grade NAT, benchmarking, and reserved/broadcast.
	extraBlockedV4 = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("240.0.0.0/4"),
	}
	siteLocalV6    = netip.MustParsePrefix("fec0::/10")
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local     = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour      = netip.MustParsePrefix("2002::/16")
)

// NewDiscordRefreshClient constructs a dedicated disgo REST client for
// POST /attachments/refresh-urls with its own rate limiter (MaxRetries=0 so 429
// responses fail fast) completely isolated from the main bot client.
func NewDiscordRefreshClient(botToken string, opts ...rest.ClientConfigOpt) rest.Client {
	slogLogger := logger.NewSlogLogger()
	baseOpts := []rest.ClientConfigOpt{
		rest.WithLogger(slogLogger),
		rest.WithUserAgent(fmt.Sprintf("DiscordBot (%s, %s)", disgo.GitHub, disgo.Version)),
		rest.WithRateLimiterConfigOpts(
			rest.WithMaxRetries(0),
			rest.WithRateLimiterLogger(slogLogger),
		),
	}
	baseOpts = append(baseOpts, opts...)
	return rest.NewClient(botToken, baseOpts...)
}

// IsDiscordAttachmentURL reports whether raw points to a real Discord attachment
// path (/attachments/ or /ephemeral-attachments/) on cdn.discordapp.com or
// media.discordapp.net. Other Discord CDN paths (emojis, stickers, avatars,
// /external/...) return false and skip /attachments/refresh-urls.
func IsDiscordAttachmentURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isAllowedLinkScheme(u.Scheme) {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "cdn.discordapp.com" && host != "media.discordapp.net" {
		return false
	}
	lowerPath := strings.ToLower(u.Path)
	if strings.HasPrefix(lowerPath, "/attachments/") && len(lowerPath) > len("/attachments/") {
		return true
	}
	if strings.HasPrefix(lowerPath, "/ephemeral-attachments/") && len(lowerPath) > len("/ephemeral-attachments/") {
		return true
	}
	return false
}

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
	refreshStatusEmpty
	refreshStatusError
)

type linkCheckOutcome int

const (
	linkAlive linkCheckOutcome = iota
	linkDead
	linkUnknown
	linkBlocked
)

func (o linkCheckOutcome) String() string {
	switch o {
	case linkAlive:
		return "alive"
	case linkDead:
		return "dead"
	case linkBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

type candidateAction int

const (
	candidatePost candidateAction = iota
	candidateDead
	candidateBlocked
	candidateTimeout
)

// LinkCheckReport holds read-only dry-run diagnostics for a single media URL.
type LinkCheckReport struct {
	URL               string
	IsAttachment      bool
	RefreshStatus     string
	RefreshedHostPath string
	HeadStatus        int
	GetStatus         int
	Verdict           string
	Error             string
}

// CheckURL runs the same attachment detection, Discord refresh-urls call, and
// SSRF-guarded HEAD/GET validation logic as live resolution in dry-run mode
// without touching or deleting any database rows.
func CheckURL(ctx context.Context, rawURL string, restClient DiscordRestClient, httpClient *http.Client) LinkCheckReport {
	trimmed := strings.TrimSpace(rawURL)
	report := LinkCheckReport{
		URL:           trimmed,
		IsAttachment:  IsDiscordAttachmentURL(trimmed),
		RefreshStatus: "skipped",
		Verdict:       linkUnknown.String(),
	}
	if httpClient == nil {
		httpClient = defaultValidationHTTPClient
	}

	targetURL := trimmed
	if report.IsAttachment {
		if restClient == nil {
			report.RefreshStatus = "unavailable"
			return report
		}
		refreshed, statusCode, outcome, err := refreshDiscordCDNURL(ctx, restClient, trimmed)
		switch outcome {
		case refreshStatusOK:
			report.RefreshStatus = "ok"
			report.RefreshedHostPath = sanitizeURLHostPath(refreshed)
			targetURL = refreshed
		case refreshStatusEmpty:
			report.RefreshStatus = "empty"
			return report
		default:
			if statusCode != 0 {
				report.RefreshStatus = fmt.Sprintf("http %d", statusCode)
			} else {
				report.RefreshStatus = "error"
			}
			if err != nil {
				report.Error = sanitizeCheckError(err)
			}
			return report
		}
	}

	outcome, headStatus, getStatus, err := checkLink(ctx, httpClient, targetURL)
	report.HeadStatus = headStatus
	report.GetStatus = getStatus
	report.Verdict = outcome.String()
	if err != nil {
		report.Error = sanitizeCheckError(err)
	}
	return report
}

func sanitizeURLHostPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host + u.Path
}

func stripURLQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		if idx := strings.IndexAny(raw, "?#"); idx >= 0 {
			return raw[:idx]
		}
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func sanitizeCheckError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var urlErr *url.Error
	for cur := err; errors.As(cur, &urlErr); cur = urlErr.Err {
		if urlErr.URL != "" {
			msg = strings.ReplaceAll(msg, urlErr.URL, stripURLQuery(urlErr.URL))
		}
	}
	return urlInErrorPattern.ReplaceAllStringFunc(msg, stripURLQuery)
}

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
// Passing nil restores the default SSRF-guarded client.
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

func (s *Service) perCheckTimeout(ctx context.Context, remainingCandidates int) (time.Duration, bool) {
	if remainingCandidates < 1 {
		remainingCandidates = 1
	}
	timeout := s.linkCheckTimeout()
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, false
		}
		slice := remaining / time.Duration(remainingCandidates)
		if slice <= 0 {
			return 0, false
		}
		if slice < timeout {
			timeout = slice
		}
	}
	return timeout, true
}

func (s *Service) validationExhausted(attempts int) bool {
	return s.cfg.ValidateLinks && attempts >= maxValidationCandidates
}

// prepareCandidate refreshes real Discord attachment URLs (when a Discord REST
// client is configured) and, when validate_links is enabled, checks liveness
// via SSRF-guarded HEAD with GET Range confirmation. Confirmed dead rows are
// queued for asynchronous deletion; blocked URLs are skipped without deleting.
func (s *Service) prepareCandidate(ctx context.Context, entry Entry, remainingCandidates int) (string, candidateAction) {
	isAttachment := IsDiscordAttachmentURL(entry.URL)
	if !s.cfg.ValidateLinks && !isAttachment {
		return entry.URL, candidatePost
	}

	restClient := s.discordRestClient()
	if !s.cfg.ValidateLinks && isAttachment && restClient == nil {
		return entry.URL, candidatePost
	}

	timeout, ok := s.perCheckTimeout(ctx, remainingCandidates)
	if !ok {
		return "", candidateTimeout
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	targetURL := entry.URL
	if isAttachment {
		if restClient == nil {
			logger.Debug("Media library Discord CDN refresh unavailable; posting canonical URL",
				zap.String("url", entry.URL),
				zap.String("guild_id", entry.GuildID))
			return entry.URL, candidatePost
		}
		refreshed, statusCode, outcome, err := refreshDiscordCDNURL(checkCtx, restClient, entry.URL)
		switch outcome {
		case refreshStatusOK:
			targetURL = refreshed
		case refreshStatusEmpty:
			logger.Debug("Media library Discord CDN refresh returned no URL; keeping row and posting canonical URL",
				zap.String("url", entry.URL),
				zap.String("guild_id", entry.GuildID))
			return entry.URL, candidatePost
		default:
			fields := []zap.Field{
				zap.String("url", entry.URL),
				zap.String("guild_id", entry.GuildID),
			}
			if statusCode != 0 {
				fields = append(fields, zap.Int("status", statusCode))
			}
			if err != nil {
				fields = append(fields, zap.Error(err))
			}
			logger.Debug("Media library Discord CDN refresh failed; keeping row and posting canonical URL", fields...)
			return entry.URL, candidatePost
		}
	}

	if !s.cfg.ValidateLinks {
		return targetURL, candidatePost
	}

	outcome, headStatus, getStatus, err := checkLink(checkCtx, s.validationHTTPClient(), targetURL)
	switch outcome {
	case linkAlive:
		return targetURL, candidatePost
	case linkDead:
		deadStatus := getStatus
		if deadStatus == 0 {
			deadStatus = headStatus
		}
		s.enqueueDeadDelete(entry, deadStatus)
		return "", candidateDead
	case linkBlocked:
		logger.Debug("Media library link validation blocked; skipping candidate",
			zap.String("url", entry.URL),
			zap.String("guild_id", entry.GuildID),
			zap.Error(err))
		return "", candidateBlocked
	default:
		fields := []zap.Field{
			zap.String("url", entry.URL),
			zap.String("guild_id", entry.GuildID),
		}
		if headStatus != 0 {
			fields = append(fields, zap.Int("head_status", headStatus))
		}
		if getStatus != 0 {
			fields = append(fields, zap.Int("get_status", getStatus))
		}
		if err != nil {
			fields = append(fields, zap.Error(err))
		}
		logger.Debug("Media library link validation inconclusive; posting URL anyway", fields...)
		return targetURL, candidatePost
	}
}

func refreshDiscordCDNURL(ctx context.Context, restClient DiscordRestClient, rawURL string) (string, int, refreshOutcome, error) {
	var resp refreshAttachmentURLsResponse
	err := restClient.Do(
		refreshAttachmentURLsEndpoint.Compile(nil),
		refreshAttachmentURLsRequest{AttachmentURLs: []string{rawURL}},
		&resp,
		rest.WithCtx(ctx),
	)
	if err != nil {
		statusCode := 0
		var restErr *rest.Error
		if errors.As(err, &restErr) && restErr.Response != nil {
			statusCode = restErr.Response.StatusCode
		}
		return "", statusCode, refreshStatusError, err
	}

	// Exactly one URL was requested, so accept refreshed_urls[0].refreshed
	// whenever non-empty regardless of how Discord normalized original.
	if len(resp.RefreshedURLs) > 0 {
		if refreshed := strings.TrimSpace(resp.RefreshedURLs[0].Refreshed); refreshed != "" {
			return refreshed, http.StatusOK, refreshStatusOK, nil
		}
	}
	return "", http.StatusOK, refreshStatusEmpty, nil
}

func checkLink(ctx context.Context, client *http.Client, targetURL string) (linkCheckOutcome, int, int, error) {
	trimmed := strings.TrimSpace(targetURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || !isAllowedLinkScheme(parsed.Scheme) {
		scheme := ""
		if parsed != nil {
			scheme = parsed.Scheme
		}
		return linkBlocked, 0, 0, fmt.Errorf("%w: unsupported URL scheme %q", errLinkBlocked, scheme)
	}

	headStatus, err := doLinkCheckRequest(ctx, client, http.MethodHead, trimmed, false)
	if err != nil {
		if errors.Is(err, errLinkBlocked) {
			return linkBlocked, 0, 0, err
		}
		return linkUnknown, 0, 0, err
	}
	if isSuccessStatus(headStatus) {
		return linkAlive, headStatus, 0, nil
	}
	if headStatus == http.StatusNotFound || headStatus == http.StatusGone ||
		headStatus == http.StatusMethodNotAllowed || headStatus == http.StatusNotImplemented {
		getStatus, err := doLinkCheckRequest(ctx, client, http.MethodGet, trimmed, true)
		if err != nil {
			if errors.Is(err, errLinkBlocked) {
				return linkBlocked, headStatus, 0, err
			}
			return linkUnknown, headStatus, 0, err
		}
		return classifyLinkStatus(getStatus), headStatus, getStatus, nil
	}
	return linkUnknown, headStatus, 0, nil
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

func isSuccessStatus(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

func classifyLinkStatus(status int) linkCheckOutcome {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return linkDead
	case isSuccessStatus(status):
		return linkAlive
	default:
		return linkUnknown
	}
}

func isAllowedLinkScheme(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func newValidationHTTPClient() *http.Client {
	return newValidationHTTPClientWithDial(
		net.DefaultResolver.LookupNetIP,
		(&net.Dialer{Timeout: defaultLinkCheckTimeout}).DialContext,
	)
}

func newValidationHTTPClientWithDial(
	lookupNetIP func(context.Context, string, string) ([]netip.Addr, error),
	dialContext func(context.Context, string, string) (net.Conn, error),
) *http.Client {
	return &http.Client{
		Transport:     newLinkCheckTransportWithDial(lookupNetIP, dialContext),
		CheckRedirect: linkCheckRedirectPolicy,
	}
}

func linkCheckRedirectPolicy(req *http.Request, via []*http.Request) error {
	if req.URL == nil || !isAllowedLinkScheme(req.URL.Scheme) {
		scheme := ""
		if req.URL != nil {
			scheme = req.URL.Scheme
		}
		return fmt.Errorf("%w: unsupported redirect scheme %q", errLinkBlocked, scheme)
	}
	if len(via) > maxLinkCheckRedirects {
		return fmt.Errorf("%w: too many redirects (max %d)", errLinkBlocked, maxLinkCheckRedirects)
	}
	return nil
}

func newLinkCheckTransportWithDial(
	lookupNetIP func(context.Context, string, string) ([]netip.Addr, error),
	dialContext func(context.Context, string, string) (net.Conn, error),
) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialTLSContext = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if blockedLinkHostName(host) {
			return nil, fmt.Errorf("%w: blocked host %q", errLinkBlocked, host)
		}

		var ips []netip.Addr
		if literalIP, parseErr := netip.ParseAddr(host); parseErr == nil {
			ips = []netip.Addr{literalIP}
		} else {
			ips, err = lookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve host %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("resolve host %q: no addresses", host)
			}
		}

		for _, ip := range ips {
			if isBlockedLinkIP(ip) {
				return nil, fmt.Errorf("%w: blocked address %s", errLinkBlocked, ip.String())
			}
		}

		var lastErr error
		for _, ip := range ips {
			conn, err := dialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("dial %q: %w", host, lastErr)
	}
	return transport
}

func blockedLinkHostName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")
}

func isBlockedLinkIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	// netip.Prefix.Contains is false for zoned addresses (fe80::1%eth0): drop the zone first.
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		for _, prefix := range extraBlockedV4 {
			if prefix.Contains(addr) {
				return true
			}
		}
	}
	if addr.Is6() {
		if siteLocalV6.Contains(addr) || nat64Local.Contains(addr) {
			return true
		}
		bytes := addr.As16()
		if nat64WellKnown.Contains(addr) &&
			isBlockedLinkIP(netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]})) {
			return true
		}
		if sixToFour.Contains(addr) &&
			isBlockedLinkIP(netip.AddrFrom4([4]byte{bytes[2], bytes[3], bytes[4], bytes[5]})) {
			return true
		}
	}
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}

// enqueueDeadDelete schedules an asynchronous dead-link deletion on the live
// worker queue. Deletes are best-effort: if the queue is full the job is
// dropped and the dead row will be retried on a later lookup.
func (s *Service) enqueueDeadDelete(entry Entry, status int) {
	s.enqueue(liveJob{
		deleteOnly:    true,
		deleteURL:     entry.URL,
		deleteGuildID: entry.GuildID,
		deleteStatus:  status,
	})
}

func (s *Service) deleteDeadEntry(rawURL, guildID string, status int) {
	delCtx, cancel := context.WithTimeout(context.Background(), deadLinkDeleteTimeout)
	defer cancel()

	if err := s.repo.DeleteEntry(delCtx, rawURL, guildID); err != nil {
		logger.Warn("Media library failed to delete dead link",
			zap.String("url", rawURL),
			zap.String("guild_id", guildID),
			zap.Int("status", status),
			zap.Error(err))
		return
	}

	logger.Info("media library dead link removed",
		zap.String("url", rawURL),
		zap.String("guild_id", guildID),
		zap.Int("status", status))
}
