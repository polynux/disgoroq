package medialibrary

import (
	"container/list"
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/logger"
)

const (
	// MaxTriggerTexts is the maximum number of recent trigger texts stored per entry.
	MaxTriggerTexts = 3

	// LiveTriggerWindow is the max age of a previous human message to count as a trigger.
	LiveTriggerWindow = 5 * time.Minute

	defaultLookupTimeout   = 3 * time.Second
	fallbackTimeoutReserve = 150 * time.Millisecond
	lookupCandidateLimit   = 5
	recentChannelScanLimit = 200
	liveQueueSize          = 128
	liveJobTimeout         = 30 * time.Second
	seenPairsCapacity      = 4096
)

type liveJob struct {
	msg        discord.Message
	updateOnly bool
}

type seenPair struct {
	messageID string
	url       string
}

// seenPairLRU is a small bounded LRU tracking (messageID, url) pairs already
// counted in live updates so MESSAGE_UPDATE unfurls never double-count uses.
type seenPairLRU struct {
	cap   int
	order *list.List
	index map[seenPair]*list.Element
}

func newSeenPairLRU(capacity int) *seenPairLRU {
	if capacity <= 0 {
		capacity = seenPairsCapacity
	}
	return &seenPairLRU{
		cap:   capacity,
		order: list.New(),
		index: make(map[seenPair]*list.Element, capacity),
	}
}

func (l *seenPairLRU) has(pair seenPair) bool {
	if elem, ok := l.index[pair]; ok {
		l.order.MoveToFront(elem)
		return true
	}
	return false
}

func (l *seenPairLRU) add(pair seenPair) {
	if elem, ok := l.index[pair]; ok {
		l.order.MoveToFront(elem)
		return
	}
	if l.order.Len() >= l.cap {
		if oldest := l.order.Back(); oldest != nil {
			l.order.Remove(oldest)
			delete(l.index, oldest.Value.(seenPair))
		}
	}
	elem := l.order.PushFront(pair)
	l.index[pair] = elem
}

// Service provides completion-mode media tag lookup, live indexing, import, and search.
type Service struct {
	repo     *Repository
	embedder Embedder
	cfg      config.MediaLibraryConfig

	workerMu sync.Mutex
	jobs     chan liveJob
	closed   bool
	workerWG sync.WaitGroup
	queueCap int

	seenMu  sync.Mutex
	seenLRU *seenPairLRU
}

// NewService creates a media library service.
func NewService(repo *Repository, embedder Embedder, cfg config.MediaLibraryConfig) *Service {
	return &Service{
		repo:     repo,
		embedder: embedder,
		cfg:      cfg,
		queueCap: liveQueueSize,
		seenLRU:  newSeenPairLRU(seenPairsCapacity),
	}
}

func (s *Service) lookupTimeout() time.Duration {
	if s.cfg.LookupTimeoutMs > 0 {
		return time.Duration(s.cfg.LookupTimeoutMs) * time.Millisecond
	}
	return defaultLookupTimeout
}

func (s *Service) startWorkerLocked() {
	capacity := s.queueCap
	if capacity <= 0 {
		capacity = liveQueueSize
	}
	jobs := make(chan liveJob, capacity)
	s.jobs = jobs
	s.workerWG.Add(1)
	go s.runWorker(jobs)
}

func (s *Service) runWorker(jobs <-chan liveJob) {
	defer s.workerWG.Done()
	for job := range jobs {
		s.processLiveJob(job)
	}
}

func (s *Service) processLiveJob(job liveJob) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Recovered panic in media library worker",
				zap.String("message_id", job.msg.ID.String()),
				zap.Any("panic", r))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), liveJobTimeout)
	defer cancel()

	var err error
	if job.updateOnly {
		err = s.RecordDiscordMessageUpdate(ctx, job.msg)
	} else {
		err = s.RecordDiscordMessage(ctx, job.msg)
	}
	if err != nil {
		logger.Warn("Media library live update failed",
			zap.String("message_id", job.msg.ID.String()),
			zap.Error(err))
	}
}

func (s *Service) enqueue(job liveJob) bool {
	if s == nil || !s.cfg.Enabled || s.repo == nil {
		return false
	}
	s.workerMu.Lock()
	if s.closed {
		s.workerMu.Unlock()
		return false
	}
	if s.jobs == nil {
		s.startWorkerLocked()
	}
	select {
	case s.jobs <- job:
		s.workerMu.Unlock()
		return true
	default:
		s.workerMu.Unlock()
		logger.Debug("Media library live queue full, dropping message",
			zap.String("message_id", job.msg.ID.String()),
			zap.Bool("update_only", job.updateOnly))
		return false
	}
}

// EnqueueMessage queues a human guild MessageCreate for serialized live indexing.
// Drops with a debug log if the bounded queue is full or after Close.
func (s *Service) EnqueueMessage(msg discord.Message) bool {
	if msg.GuildID == nil || msg.Author.Bot || msg.WebhookID != nil {
		return false
	}
	return s.enqueue(liveJob{msg: msg, updateOnly: false})
}

// EnqueueMessageUpdate queues a human guild MessageUpdate with embeds for
// serialized live indexing of unfurled link descriptions.
func (s *Service) EnqueueMessageUpdate(msg discord.Message) bool {
	// Update payloads can be partial: without a known author we cannot tell a human from a bot.
	if msg.GuildID == nil || msg.Author.ID == 0 || msg.Author.Bot || msg.WebhookID != nil || len(msg.Embeds) == 0 {
		return false
	}
	return s.enqueue(liveJob{msg: msg, updateOnly: true})
}

// Close stops accepting new live-indexing jobs and drains all queued jobs before returning.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.workerMu.Lock()
	if s.closed {
		s.workerMu.Unlock()
		return
	}
	s.closed = true
	if s.jobs != nil {
		close(s.jobs)
	}
	s.workerMu.Unlock()
	s.workerWG.Wait()
}

type resolveFailureReason string

const (
	reasonNoMatch   resolveFailureReason = "no_match"
	reasonJunk      resolveFailureReason = "junk"
	reasonError     resolveFailureReason = "error"
	gifOnRequestTag                      = "gif_on_request"
)

var (
	gifRequestPlaceholderPattern = regexp.MustCompile(`(?i)\[(?:gif|lien|image|youtube|tweet|tiktok|insta|son|steam|video|fichier|sticker)(?::[^\]]*)?\]`)
	gifRequestURLPattern         = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s\p{Z}]+`)
	gifRequestEmojiPattern       = regexp.MustCompile(`<a?:\w+:\d+>`)
	gifRequestWordPattern        = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])gifs?(?:$|[^\p{L}\p{N}_])`)
)

// IsGifRequest reports whether text explicitly asks for a gif (standalone
// case-insensitive word "gif" or "gifs"), ignoring media placeholder tags,
// raw URLs, and Discord custom emoji markup.
func IsGifRequest(text string) bool {
	cleaned := gifRequestPlaceholderPattern.ReplaceAllString(text, " ")
	cleaned = gifRequestURLPattern.ReplaceAllString(cleaned, " ")
	cleaned = gifRequestEmojiPattern.ReplaceAllString(cleaned, " ")
	return gifRequestWordPattern.MatchString(cleaned)
}

// Resolve implements ai.MediaResolver. It runs under a dedicated short timeout
// independent of the completion context so a slow or hung embedder only strips
// tags and never consumes the completion retry budget.
func (s *Service) Resolve(ctx context.Context, req ai.MediaResolveRequest) (ai.MediaResolveResult, bool) {
	if s == nil || !s.cfg.Enabled || s.repo == nil || req.GuildID == "" {
		return ai.MediaResolveResult{}, false
	}

	tags := ai.ParseMediaTags(req.RawReply)
	gifRequested := s.cfg.GifOnRequest && IsGifRequest(req.TargetText)
	if len(tags) == 0 && !gifRequested {
		return ai.MediaResolveResult{}, false
	}

	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	lookupCtx, cancel := context.WithTimeout(baseCtx, s.lookupTimeout())
	defer cancel()

	excludeList, recentURLs := s.recentBotURLs(lookupCtx, req.ChannelID, req.BotID)
	filter := LookupFilter{
		AllowDiscordCDN: s.cfg.AllowDiscordCDN,
		ExcludeURLs:     excludeList,
	}

	var (
		fallbackTag     string
		fallbackReason  resolveFailureReason
		hasUnmatchedGif bool
		triedBareGif    bool
	)
	recordGifFailure := func(tagRaw string, reason resolveFailureReason) {
		if !hasUnmatchedGif {
			hasUnmatchedGif = true
			fallbackTag = tagRaw
			fallbackReason = reason
		}
	}

	for _, tag := range tags {
		if lookupCtx.Err() != nil {
			logger.Warn("Media library lookup timed out",
				zap.String("tag", tag.Raw),
				zap.String("guild_id", req.GuildID),
				zap.Error(lookupCtx.Err()))
			if tag.Kind == "gif" {
				recordGifFailure(tag.Raw, reasonError)
			}
			break
		}
		if tag.Description != "" {
			if tag.Kind == "gif" && s.cfg.RandomGifFallback && isJunkGifDescription(tag.Description) {
				recordGifFailure(tag.Raw, reasonJunk)
				continue
			}
			reserve := s.cfg.RandomGifFallback && (tag.Kind == "gif" || gifRequested)
			if result, ok, hadErr := s.resolveDescribedTag(lookupCtx, req.GuildID, tag, filter, recentURLs, reserve); ok {
				return result, true
			} else if tag.Kind == "gif" {
				if hadErr {
					recordGifFailure(tag.Raw, reasonError)
				} else {
					recordGifFailure(tag.Raw, reasonNoMatch)
				}
			}
			continue
		}
		if tag.Kind == "gif" {
			triedBareGif = true
			reserve := s.cfg.RandomGifFallback
			if result, ok, hadErr := s.resolveBareGifTag(lookupCtx, req.GuildID, tag, req.TargetText, filter, recentURLs, reserve); ok {
				return result, true
			} else if hadErr {
				recordGifFailure(tag.Raw, reasonError)
			} else {
				recordGifFailure(tag.Raw, reasonNoMatch)
			}
		}
	}

	triedRandomFallback := false
	if hasUnmatchedGif && s.cfg.RandomGifFallback {
		triedRandomFallback = true
		if result, ok := s.resolveRandomGifFallback(lookupCtx, req.GuildID, fallbackTag, fallbackReason, filter, recentURLs); ok {
			return result, true
		}
	}

	if gifRequested {
		reqTag := ai.MediaTag{Raw: gifOnRequestTag, Kind: "gif"}
		reason := reasonNoMatch
		if !triedBareGif {
			if result, ok, hadErr := s.resolveBareGifTag(lookupCtx, req.GuildID, reqTag, req.TargetText, filter, recentURLs, s.cfg.RandomGifFallback); ok {
				return result, true
			} else if hadErr {
				reason = reasonError
			}
		} else if hasUnmatchedGif {
			reason = fallbackReason
		}
		if s.cfg.RandomGifFallback && !triedRandomFallback {
			if result, ok := s.resolveRandomGifFallback(lookupCtx, req.GuildID, reqTag.Raw, reason, filter, recentURLs); ok {
				return result, true
			}
		}
	}

	return ai.MediaResolveResult{}, false
}

// embedContext returns a context for embedding calls that optionally reserves a
// small slice of lookupCtx's deadline so a local random-gif fallback query can
// still run within the overall lookup timeout when the embedder hangs.
func (s *Service) embedContext(lookupCtx context.Context, reserveForFallback bool) (context.Context, context.CancelFunc, bool) {
	if !reserveForFallback {
		return lookupCtx, func() {}, true
	}
	deadline, ok := lookupCtx.Deadline()
	if !ok {
		return lookupCtx, func() {}, true
	}
	reserve := fallbackTimeoutReserve
	if quarter := s.lookupTimeout() / 4; quarter < reserve {
		reserve = quarter
	}
	remaining := time.Until(deadline)
	if remaining <= reserve {
		return lookupCtx, func() {}, false
	}
	embedCtx, cancel := context.WithTimeout(lookupCtx, remaining-reserve)
	return embedCtx, cancel, true
}

func (s *Service) resolveRandomGifFallback(ctx context.Context, guildID, tagRaw string, reason resolveFailureReason, filter LookupFilter, recentURLs map[string]bool) (ai.MediaResolveResult, bool) {
	if ctx.Err() != nil {
		logger.Warn("Media library random gif fallback skipped: lookup timed out",
			zap.String("tag", tagRaw),
			zap.String("reason", string(reason)),
			zap.String("guild_id", guildID),
			zap.Error(ctx.Err()))
		return ai.MediaResolveResult{}, false
	}
	entries, err := s.repo.FindRandomGif(ctx, guildID, filter, lookupCandidateLimit)
	if err != nil {
		logger.Warn("Media library random gif fallback query failed",
			zap.String("tag", tagRaw),
			zap.String("reason", string(reason)),
			zap.String("guild_id", guildID),
			zap.Error(err))
		return ai.MediaResolveResult{}, false
	}
	chosen, ok := firstEligibleEntry(entries, func(u string) bool { return s.eligible(u, recentURLs) })
	if !ok {
		logger.Info("Media library random gif fallback found no eligible gif",
			zap.String("tag", tagRaw),
			zap.String("reason", string(reason)),
			zap.String("guild_id", guildID))
		return ai.MediaResolveResult{}, false
	}
	logger.Info("Media library random gif fallback",
		zap.String("tag", tagRaw),
		zap.String("reason", string(reason)),
		zap.String("url", chosen.URL),
		zap.String("guild_id", guildID))
	return ai.MediaResolveResult{Tag: tagRaw, URL: chosen.URL}, true
}

var junkGifStrongTokens = map[string]bool{
	"ezgif":      true,
	"togif":      true,
	"gifmaker":   true,
	"gifbrewery": true,
}

var junkGifNoiseTokens = map[string]bool{
	// Platforms / domains / URL artifacts
	"tenor": true, "giphy": true, "klipy": true, "gfycat": true,
	"imgur": true, "imgflip": true, "kapwing": true,
	"discord": true, "discordapp": true, "cdn": true,
	"com": true, "net": true, "org": true, "www": true,
	"http": true, "https": true, "html": true, "php": true,
	// Formats / extensions
	"gif": true, "gifs": true, "apng": true, "webp": true,
	"png": true, "jpg": true, "jpeg": true, "mp4": true,
	"webm": true, "mov": true, "avi": true, "mkv": true,
	"svg": true, "bmp": true, "heic": true,
	// Converter / generic file artifacts
	"converter": true, "convert": true, "maker": true,
	"resize": true, "resized": true, "crop": true, "cropped": true,
	"optimize": true, "optimized": true, "compress": true, "compressed": true,
	"reverse": true, "reversed": true, "rotate": true, "rotated": true,
	"trim": true, "trimmed": true, "split": true, "overlay": true,
	"output": true, "export": true, "untitled": true, "unknown": true,
	"attachment": true, "attachments": true, "image": true, "img": true,
	"video": true, "vid": true, "media": true, "file": true, "fichier": true,
	"download": true, "caption": true, "view": true, "thumbnail": true,
	"thumb": true, "frame": true, "frames": true,
	// Short connectors that carry no meaning on their own
	"to": true, "from": true, "by": true, "of": true, "in": true, "on": true,
	"at": true, "the": true, "a": true, "an": true, "de": true, "du": true,
	"la": true, "le": true, "en": true, "et": true,
}

var junkGifNumberedPrefixes = []string{"img", "image", "video", "vid", "gif", "file", "clip", "ezgif"}

// isJunkGifDescription reports whether a gif tag description consists of
// uploaded-filename / converter leftovers (e.g. "togif", "ezgif com apng to converter")
// or non-semantic noise/IDs that will never match meaningful media.
func isJunkGifDescription(desc string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(desc), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(tokens) == 0 {
		return true
	}
	hasMeaningful := false
	for _, tok := range tokens {
		if junkGifStrongTokens[tok] {
			return true
		}
		if !isJunkGifToken(tok) {
			hasMeaningful = true
		}
	}
	return !hasMeaningful
}

func isJunkGifToken(tok string) bool {
	if junkGifNoiseTokens[tok] || junkGifStrongTokens[tok] {
		return true
	}
	runeCount := utf8.RuneCountInString(tok)
	if runeCount <= 1 {
		return true
	}
	allDigits := true
	hasDigit := false
	allHex := true
	for _, r := range tok {
		isDig := r >= '0' && r <= '9'
		if isDig {
			hasDigit = true
		} else {
			allDigits = false
		}
		if !isDig && !(r >= 'a' && r <= 'f') {
			allHex = false
		}
	}
	if allDigits {
		return true
	}
	if runeCount >= 8 && hasDigit {
		return true
	}
	if allHex && runeCount >= 6 && hasDigit {
		return true
	}
	for _, prefix := range junkGifNumberedPrefixes {
		if strings.HasPrefix(tok, prefix) && len(tok) > len(prefix) {
			rest := tok[len(prefix):]
			restDigits := true
			for _, r := range rest {
				if r < '0' || r > '9' {
					restDigits = false
					break
				}
			}
			if restDigits {
				return true
			}
		}
	}
	return false
}

func (s *Service) eligible(rawURL string, recentURLs map[string]bool) bool {
	if !s.cfg.AllowDiscordCDN && ai.IsDiscordCDNURL(rawURL) {
		return false
	}
	if recentURLs[ai.CanonicalizeMediaURL(rawURL)] {
		return false
	}
	return true
}

func (s *Service) recentBotURLs(ctx context.Context, channelID, botID string) ([]string, map[string]bool) {
	if s.cfg.RepeatWindow <= 0 || channelID == "" || botID == "" {
		return nil, nil
	}
	messages, err := s.repo.GetRecentBotMessages(ctx, channelID, botID, s.cfg.RepeatWindow)
	if err != nil {
		logger.Warn("Media library failed to read recent bot messages for repeat window",
			zap.String("channel_id", channelID),
			zap.Error(err))
		return nil, nil
	}
	if len(messages) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(messages))
	list := make([]string, 0, len(messages))
	for _, content := range messages {
		for _, u := range ai.ExtractAllURLs(content) {
			if !seen[u] {
				seen[u] = true
				list = append(list, u)
			}
		}
	}
	return list, seen
}

func (s *Service) resolveDescribedTag(ctx context.Context, guildID string, tag ai.MediaTag, filter LookupFilter, recentURLs map[string]bool, reserveForFallback bool) (ai.MediaResolveResult, bool, bool) {
	hadErr := false

	// 1. Exact description match in the same guild (ordered by uses DESC, last_seen DESC).
	exactRows, err := s.repo.FindExactDescription(ctx, guildID, tag.Kind, tag.Description, filter, lookupCandidateLimit)
	if err != nil {
		hadErr = true
		logger.Warn("Media library exact description lookup failed",
			zap.String("tag", tag.Raw),
			zap.String("guild_id", guildID),
			zap.Error(err))
	} else if chosen, ok := firstEligibleEntry(exactRows, func(u string) bool { return s.eligible(u, recentURLs) }); ok {
		logger.Info("Media library matched tag",
			zap.String("tag", tag.Raw),
			zap.String("url", chosen.URL),
			zap.Float64("similarity", 1.0),
			zap.String("match_type", "exact"),
			zap.String("guild_id", guildID),
			zap.Bool("cross_guild", false))
		return ai.MediaResolveResult{Tag: tag.Raw, URL: chosen.URL, Similarity: 1.0}, true, false
	}

	// 2. Semantic vector search in the same guild.
	var queryBlob []byte
	bestSim := 0.0
	if s.embedder != nil && ctx.Err() == nil {
		embedCtx, embedCancel, hasBudget := s.embedContext(ctx, reserveForFallback)
		if !hasBudget {
			hadErr = true
			logger.Warn("Media library skipped embedding: lookup timeout budget reserved for fallback",
				zap.String("tag", tag.Raw),
				zap.String("guild_id", guildID))
		} else {
			queryBlob, err = EmbedQuery(embedCtx, s.embedder, tag.Description)
			embedCancel()
			if err != nil {
				hadErr = true
				logger.Warn("Media library failed to embed tag description",
					zap.String("tag", tag.Raw),
					zap.String("guild_id", guildID),
					zap.Error(err))
			} else {
				hits, err := s.repo.SearchByDescription(ctx, guildID, tag.Kind, queryBlob, filter, lookupCandidateLimit)
				if err != nil {
					hadErr = true
					logger.Warn("Media library description vector search failed",
						zap.String("tag", tag.Raw),
						zap.String("guild_id", guildID),
						zap.Error(err))
				} else if hit, ok := firstEligibleHit(hits, func(u string) bool { return s.eligible(u, recentURLs) }); ok {
					if hit.Similarity > bestSim {
						bestSim = hit.Similarity
					}
					if hit.Similarity >= s.cfg.MinSimilarity {
						logger.Info("Media library matched tag",
							zap.String("tag", tag.Raw),
							zap.String("url", hit.Entry.URL),
							zap.Float64("similarity", hit.Similarity),
							zap.String("match_type", "description"),
							zap.String("guild_id", guildID),
							zap.Bool("cross_guild", false))
						return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true, false
					}
				}
			}
		}
	}

	// 3. Optional cross-guild fallback (exact, then vector).
	if s.cfg.CrossGuild && ctx.Err() == nil {
		crossExact, err := s.repo.FindExactDescriptionCrossGuild(ctx, guildID, tag.Kind, tag.Description, filter, lookupCandidateLimit)
		if err != nil {
			hadErr = true
			logger.Warn("Media library cross-guild exact lookup failed",
				zap.String("tag", tag.Raw),
				zap.String("guild_id", guildID),
				zap.Error(err))
		} else if chosen, ok := firstEligibleEntry(crossExact, func(u string) bool { return s.eligible(u, recentURLs) }); ok {
			logger.Info("Media library matched tag",
				zap.String("tag", tag.Raw),
				zap.String("url", chosen.URL),
				zap.Float64("similarity", 1.0),
				zap.String("match_type", "exact"),
				zap.String("guild_id", guildID),
				zap.Bool("cross_guild", true))
			return ai.MediaResolveResult{Tag: tag.Raw, URL: chosen.URL, Similarity: 1.0}, true, false
		}

		if len(queryBlob) > 0 && ctx.Err() == nil {
			crossHits, err := s.repo.SearchByDescriptionCrossGuild(ctx, guildID, tag.Kind, queryBlob, filter, lookupCandidateLimit)
			if err != nil {
				hadErr = true
				logger.Warn("Media library cross-guild description search failed",
					zap.String("tag", tag.Raw),
					zap.String("guild_id", guildID),
					zap.Error(err))
			} else if hit, ok := firstEligibleHit(crossHits, func(u string) bool { return s.eligible(u, recentURLs) }); ok {
				if hit.Similarity > bestSim {
					bestSim = hit.Similarity
				}
				if hit.Similarity >= s.cfg.MinSimilarity {
					logger.Info("Media library matched tag",
						zap.String("tag", tag.Raw),
						zap.String("url", hit.Entry.URL),
						zap.Float64("similarity", hit.Similarity),
						zap.String("match_type", "description"),
						zap.String("guild_id", guildID),
						zap.Bool("cross_guild", true))
					return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true, false
				}
			}
		}
	}

	logger.Info("Media library no match",
		zap.String("tag", tag.Raw),
		zap.Float64("best_similarity", bestSim),
		zap.Float64("threshold", s.cfg.MinSimilarity),
		zap.String("guild_id", guildID))
	return ai.MediaResolveResult{}, false, hadErr
}

func (s *Service) resolveBareGifTag(ctx context.Context, guildID string, tag ai.MediaTag, targetText string, filter LookupFilter, recentURLs map[string]bool, reserveForFallback bool) (ai.MediaResolveResult, bool, bool) {
	trimmedTarget := strings.TrimSpace(targetText)
	if trimmedTarget == "" || s.embedder == nil {
		logger.Info("Media library no match",
			zap.String("tag", tag.Raw),
			zap.Float64("best_similarity", 0),
			zap.Float64("threshold", s.cfg.MinTriggerSimilarity),
			zap.String("guild_id", guildID))
		return ai.MediaResolveResult{}, false, false
	}

	embedCtx, embedCancel, hasBudget := s.embedContext(ctx, reserveForFallback)
	if !hasBudget {
		logger.Warn("Media library skipped trigger embedding: lookup timeout budget reserved for fallback",
			zap.String("tag", tag.Raw),
			zap.String("guild_id", guildID))
		return ai.MediaResolveResult{}, false, true
	}
	queryBlob, err := EmbedQuery(embedCtx, s.embedder, trimmedTarget)
	embedCancel()
	if err != nil {
		logger.Warn("Media library failed to embed trigger query",
			zap.String("tag", tag.Raw),
			zap.String("guild_id", guildID),
			zap.Error(err))
		return ai.MediaResolveResult{}, false, true
	}

	hadErr := false
	bestSim := 0.0
	hits, err := s.repo.SearchByTrigger(ctx, guildID, "gif", queryBlob, filter, lookupCandidateLimit)
	if err != nil {
		hadErr = true
		logger.Warn("Media library trigger vector search failed",
			zap.String("tag", tag.Raw),
			zap.String("guild_id", guildID),
			zap.Error(err))
	} else if hit, ok := firstEligibleHit(hits, func(u string) bool { return s.eligible(u, recentURLs) }); ok {
		if hit.Similarity > bestSim {
			bestSim = hit.Similarity
		}
		if hit.Similarity >= s.cfg.MinTriggerSimilarity {
			logger.Info("Media library matched tag",
				zap.String("tag", tag.Raw),
				zap.String("url", hit.Entry.URL),
				zap.Float64("similarity", hit.Similarity),
				zap.String("match_type", "trigger"),
				zap.String("guild_id", guildID),
				zap.Bool("cross_guild", false))
			return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true, false
		}
	}

	if s.cfg.CrossGuild && ctx.Err() == nil {
		crossHits, err := s.repo.SearchByTriggerCrossGuild(ctx, guildID, "gif", queryBlob, filter, lookupCandidateLimit)
		if err != nil {
			hadErr = true
			logger.Warn("Media library cross-guild trigger search failed",
				zap.String("tag", tag.Raw),
				zap.String("guild_id", guildID),
				zap.Error(err))
		} else if hit, ok := firstEligibleHit(crossHits, func(u string) bool { return s.eligible(u, recentURLs) }); ok {
			if hit.Similarity > bestSim {
				bestSim = hit.Similarity
			}
			if hit.Similarity >= s.cfg.MinTriggerSimilarity {
				logger.Info("Media library matched tag",
					zap.String("tag", tag.Raw),
					zap.String("url", hit.Entry.URL),
					zap.Float64("similarity", hit.Similarity),
					zap.String("match_type", "trigger"),
					zap.String("guild_id", guildID),
					zap.Bool("cross_guild", true))
				return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true, false
			}
		}
	}

	logger.Info("Media library no match",
		zap.String("tag", tag.Raw),
		zap.Float64("best_similarity", bestSim),
		zap.Float64("threshold", s.cfg.MinTriggerSimilarity),
		zap.String("guild_id", guildID))
	return ai.MediaResolveResult{}, false, hadErr
}

func firstEligibleEntry(entries []Entry, okFn func(string) bool) (Entry, bool) {
	for _, entry := range entries {
		if okFn(entry.URL) {
			return entry, true
		}
	}
	return Entry{}, false
}

func firstEligibleHit(hits []SearchHit, okFn func(string) bool) (SearchHit, bool) {
	for _, hit := range hits {
		if okFn(hit.Entry.URL) {
			return hit, true
		}
	}
	return SearchHit{}, false
}

func (s *Service) hasSeenPair(messageID, url string) bool {
	if messageID == "" {
		return false
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if s.seenLRU == nil {
		s.seenLRU = newSeenPairLRU(seenPairsCapacity)
	}
	return s.seenLRU.has(seenPair{messageID: messageID, url: url})
}

func (s *Service) markSeenPair(messageID, url string) {
	if messageID == "" {
		return
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if s.seenLRU == nil {
		s.seenLRU = newSeenPairLRU(seenPairsCapacity)
	}
	s.seenLRU.add(seenPair{messageID: messageID, url: url})
}

// RecordDiscordMessage indexes gif and described-link URLs from a human guild
// message create event, updating counts, timestamps, trigger texts, and embeddings.
func (s *Service) RecordDiscordMessage(ctx context.Context, msg discord.Message) error {
	return s.recordDiscordMessage(ctx, msg, false)
}

// RecordDiscordMessageUpdate indexes described non-gif links whose embeds arrived
// in a MESSAGE_UPDATE event without double-counting uses for already-indexed pairs.
func (s *Service) RecordDiscordMessageUpdate(ctx context.Context, msg discord.Message) error {
	return s.recordDiscordMessage(ctx, msg, true)
}

func (s *Service) recordDiscordMessage(ctx context.Context, msg discord.Message, updateOnly bool) error {
	if s == nil || !s.cfg.Enabled || s.repo == nil {
		return nil
	}
	if msg.GuildID == nil || msg.Author.Bot || msg.WebhookID != nil {
		return nil
	}
	if updateOnly && len(msg.Embeds) == 0 {
		return nil
	}

	items := ai.ExtractMediaURLs(ai.MessageFromDiscord(msg))
	if updateOnly {
		filtered := items[:0]
		for _, item := range items {
			if item.Kind != "gif" && item.Description != "" {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) == 0 {
		return nil
	}

	msgID := ""
	if msg.ID != 0 {
		msgID = msg.ID.String()
	}

	pending := make([]ai.ExtractedMedia, 0, len(items))
	for _, item := range items {
		if s.hasSeenPair(msgID, item.URL) {
			continue
		}
		pending = append(pending, item)
	}
	if len(pending) == 0 {
		return nil
	}

	guildID := msg.GuildID.String()
	msgTime := discordMessageTime(msg)
	ts := msgTime.Unix()
	newTrigger := s.resolveLiveTriggerText(ctx, msg, msgTime)

	for _, item := range pending {
		existing, err := s.repo.GetEntry(ctx, item.URL, guildID)
		if err != nil {
			logger.Warn("Media library failed to load existing entry before live update; skipping URL",
				zap.String("url", item.URL),
				zap.String("guild_id", guildID),
				zap.Error(err))
			continue
		}

		storedDesc := item.Description
		effectiveDesc := item.Description
		existingTrigger := ""
		if existing != nil {
			if effectiveDesc == "" {
				effectiveDesc = existing.Description
			}
			existingTrigger = existing.TriggerText
		}
		mergedTrigger := prependTriggerText(existingTrigger, newTrigger, MaxTriggerTexts)
		storedTrigger := mergedTrigger

		var descEmb []byte
		if effectiveDesc != "" && s.embedder != nil &&
			(existing == nil || existing.Description != effectiveDesc || len(existing.DescriptionEmbedding) == 0) {
			descEmb, err = EmbedDocument(ctx, s.embedder, effectiveDesc)
			if err != nil {
				logger.Warn("Media library failed to compute description embedding",
					zap.String("url", item.URL),
					zap.String("guild_id", guildID),
					zap.Error(err))
				// Never advance description past a failed embedding when an existing embedding is valid.
				if existing != nil && existing.Description != effectiveDesc && len(existing.DescriptionEmbedding) > 0 {
					storedDesc = existing.Description
				}
			}
		}

		var trigEmb []byte
		if mergedTrigger != "" && s.embedder != nil &&
			(existing == nil || existing.TriggerText != mergedTrigger || len(existing.TriggerEmbedding) == 0) {
			trigEmb, err = EmbedDocument(ctx, s.embedder, mergedTrigger)
			if err != nil {
				logger.Warn("Media library failed to compute trigger embedding",
					zap.String("url", item.URL),
					zap.String("guild_id", guildID),
					zap.Error(err))
				// Never advance trigger_text past a failed embedding when an existing embedding is valid.
				if existing != nil && existing.TriggerText != mergedTrigger && len(existing.TriggerEmbedding) > 0 {
					storedTrigger = existing.TriggerText
				}
			}
		}

		if err := s.repo.UpsertLive(ctx, Entry{
			GuildID:              guildID,
			URL:                  item.URL,
			Kind:                 item.Kind,
			Description:          storedDesc,
			FirstSeen:            ts,
			LastSeen:             ts,
			TriggerText:          storedTrigger,
			DescriptionEmbedding: descEmb,
			TriggerEmbedding:     trigEmb,
		}); err != nil {
			logger.Warn("Media library failed to upsert live entry",
				zap.String("url", item.URL),
				zap.String("guild_id", guildID),
				zap.Error(err))
			continue
		}

		s.markSeenPair(msgID, item.URL)
	}

	return nil
}

func (s *Service) resolveLiveTriggerText(ctx context.Context, msg discord.Message, msgTime time.Time) string {
	// 1. Replied-to message takes priority (plain v1 render, matching exporter).
	if msg.MessageReference != nil && msg.MessageReference.MessageID != nil {
		refMsg := msg.ReferencedMessage
		if refMsg == nil {
			cached, err := s.repo.GetDiscordMessageByID(ctx, msg.MessageReference.MessageID.String())
			if err == nil {
				refMsg = cached
			}
		}
		if refMsg != nil {
			if rendered := strings.TrimSpace(ai.RenderMediaMessage(ai.MessageFromDiscord(*refMsg), ai.MediaTagsV1)); rendered != "" {
				return rendered
			}
		}
	}

	// 2. Previous human message in the same channel within 5 minutes (bounded scan).
	recent, err := s.repo.GetRecentDiscordMessagesByChannel(ctx, msg.ChannelID.String(), recentChannelScanLimit)
	if err != nil {
		return ""
	}
	for _, cand := range recent {
		if cand.ID == msg.ID {
			continue
		}
		if cand.Author.Bot || cand.WebhookID != nil {
			continue
		}
		candTime := discordMessageTime(cand)
		if candTime.After(msgTime) {
			continue
		}
		if msgTime.Sub(candTime) > LiveTriggerWindow {
			break
		}
		if rendered := strings.TrimSpace(ai.RenderMediaMessage(ai.MessageFromDiscord(cand), ai.MediaTagsV1)); rendered != "" {
			return rendered
		}
	}
	return ""
}

// Search performs a read-only vector search in guildID across all kinds (for CLI tuning).
func (s *Service) Search(ctx context.Context, guildID, query string, useTrigger bool, limit int) ([]SearchHit, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("media library repository is not initialized")
	}
	if strings.TrimSpace(guildID) == "" {
		return nil, fmt.Errorf("guild ID is required")
	}
	if limit <= 0 {
		limit = 10
	}
	queryBlob, err := EmbedQuery(ctx, s.embedder, query)
	if err != nil {
		return nil, err
	}
	if useTrigger {
		return s.repo.SearchAllKindsByTrigger(ctx, guildID, queryBlob, limit)
	}
	return s.repo.SearchAllKindsByDescription(ctx, guildID, queryBlob, limit)
}

func discordMessageTime(m discord.Message) time.Time {
	if !m.CreatedAt.IsZero() {
		return m.CreatedAt
	}
	if m.ID != 0 {
		return m.ID.Time()
	}
	return time.Now()
}

// prependTriggerText prepends newTrigger to newline-separated existing triggers,
// deduplicating identical lines and keeping at most maxItems most recent entries.
func prependTriggerText(existing, newTrigger string, maxItems int) string {
	newTrigger = strings.TrimSpace(strings.ReplaceAll(newTrigger, "\n", " "))
	lines := make([]string, 0, maxItems)
	seen := make(map[string]bool, maxItems)
	if newTrigger != "" {
		lines = append(lines, newTrigger)
		seen[newTrigger] = true
	}
	if existing != "" {
		for _, line := range strings.Split(existing, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			lines = append(lines, line)
			seen[line] = true
			if len(lines) >= maxItems {
				break
			}
		}
	}
	if len(lines) > maxItems {
		lines = lines[:maxItems]
	}
	return strings.Join(lines, "\n")
}
