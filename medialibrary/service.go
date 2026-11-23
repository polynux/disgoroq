package medialibrary

import (
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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
	if msg.GuildID == nil || msg.Author.Bot || msg.WebhookID != nil || len(msg.Embeds) == 0 {
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

// Resolve implements ai.MediaResolver. It runs under a dedicated short timeout
// independent of the completion context so a slow or hung embedder only strips
// tags and never consumes the completion retry budget.
func (s *Service) Resolve(ctx context.Context, req ai.MediaResolveRequest) (ai.MediaResolveResult, bool) {
	if s == nil || !s.cfg.Enabled || s.repo == nil || req.GuildID == "" {
		return ai.MediaResolveResult{}, false
	}

	tags := ai.ParseMediaTags(req.RawReply)
	if len(tags) == 0 {
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

	for _, tag := range tags {
		if lookupCtx.Err() != nil {
			logger.Warn("Media library lookup timed out",
				zap.String("tag", tag.Raw),
				zap.String("guild_id", req.GuildID),
				zap.Error(lookupCtx.Err()))
			return ai.MediaResolveResult{}, false
		}
		if tag.Description != "" {
			if result, ok := s.resolveDescribedTag(lookupCtx, req.GuildID, tag, filter, recentURLs); ok {
				return result, true
			}
			continue
		}
		if tag.Kind == "gif" {
			if result, ok := s.resolveBareGifTag(lookupCtx, req.GuildID, tag, req.TargetText, filter, recentURLs); ok {
				return result, true
			}
		}
	}

	return ai.MediaResolveResult{}, false
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

func (s *Service) resolveDescribedTag(ctx context.Context, guildID string, tag ai.MediaTag, filter LookupFilter, recentURLs map[string]bool) (ai.MediaResolveResult, bool) {
	// 1. Exact description match in the same guild (ordered by uses DESC, last_seen DESC).
	exactRows, err := s.repo.FindExactDescription(ctx, guildID, tag.Kind, tag.Description, filter, lookupCandidateLimit)
	if err != nil {
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
		return ai.MediaResolveResult{Tag: tag.Raw, URL: chosen.URL, Similarity: 1.0}, true
	}

	// 2. Semantic vector search in the same guild.
	var queryBlob []byte
	bestSim := 0.0
	if s.embedder != nil {
		queryBlob, err = EmbedQuery(ctx, s.embedder, tag.Description)
		if err != nil {
			logger.Warn("Media library failed to embed tag description",
				zap.String("tag", tag.Raw),
				zap.String("guild_id", guildID),
				zap.Error(err))
		} else {
			hits, err := s.repo.SearchByDescription(ctx, guildID, tag.Kind, queryBlob, filter, lookupCandidateLimit)
			if err != nil {
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
					return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true
				}
			}
		}
	}

	// 3. Optional cross-guild fallback (exact, then vector).
	if s.cfg.CrossGuild && ctx.Err() == nil {
		crossExact, err := s.repo.FindExactDescriptionCrossGuild(ctx, guildID, tag.Kind, tag.Description, filter, lookupCandidateLimit)
		if err != nil {
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
			return ai.MediaResolveResult{Tag: tag.Raw, URL: chosen.URL, Similarity: 1.0}, true
		}

		if len(queryBlob) > 0 && ctx.Err() == nil {
			crossHits, err := s.repo.SearchByDescriptionCrossGuild(ctx, guildID, tag.Kind, queryBlob, filter, lookupCandidateLimit)
			if err != nil {
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
					return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true
				}
			}
		}
	}

	logger.Info("Media library no match",
		zap.String("tag", tag.Raw),
		zap.Float64("best_similarity", bestSim),
		zap.Float64("threshold", s.cfg.MinSimilarity),
		zap.String("guild_id", guildID))
	return ai.MediaResolveResult{}, false
}

func (s *Service) resolveBareGifTag(ctx context.Context, guildID string, tag ai.MediaTag, targetText string, filter LookupFilter, recentURLs map[string]bool) (ai.MediaResolveResult, bool) {
	trimmedTarget := strings.TrimSpace(targetText)
	if trimmedTarget == "" || s.embedder == nil {
		logger.Info("Media library no match",
			zap.String("tag", tag.Raw),
			zap.Float64("best_similarity", 0),
			zap.Float64("threshold", s.cfg.MinTriggerSimilarity),
			zap.String("guild_id", guildID))
		return ai.MediaResolveResult{}, false
	}

	queryBlob, err := EmbedQuery(ctx, s.embedder, trimmedTarget)
	if err != nil {
		logger.Warn("Media library failed to embed trigger query",
			zap.String("tag", tag.Raw),
			zap.String("guild_id", guildID),
			zap.Error(err))
		return ai.MediaResolveResult{}, false
	}

	bestSim := 0.0
	hits, err := s.repo.SearchByTrigger(ctx, guildID, "gif", queryBlob, filter, lookupCandidateLimit)
	if err != nil {
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
			return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true
		}
	}

	if s.cfg.CrossGuild && ctx.Err() == nil {
		crossHits, err := s.repo.SearchByTriggerCrossGuild(ctx, guildID, "gif", queryBlob, filter, lookupCandidateLimit)
		if err != nil {
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
				return ai.MediaResolveResult{Tag: tag.Raw, URL: hit.Entry.URL, Similarity: hit.Similarity}, true
			}
		}
	}

	logger.Info("Media library no match",
		zap.String("tag", tag.Raw),
		zap.Float64("best_similarity", bestSim),
		zap.Float64("threshold", s.cfg.MinTriggerSimilarity),
		zap.String("guild_id", guildID))
	return ai.MediaResolveResult{}, false
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
