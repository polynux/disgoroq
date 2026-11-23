package medialibrary

import (
	"context"
	"fmt"
	"strings"
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

	recentChannelScanLimit = 20
)

// Service provides completion-mode media tag lookup, live indexing, import, and search.
type Service struct {
	repo     *Repository
	embedder Embedder
	cfg      config.MediaLibraryConfig
}

// NewService creates a media library service.
func NewService(repo *Repository, embedder Embedder, cfg config.MediaLibraryConfig) *Service {
	return &Service{
		repo:     repo,
		embedder: embedder,
		cfg:      cfg,
	}
}

// Resolve implements ai.MediaResolver. It inspects resolvable media tags in
// req.RawReply and returns the first matching URL from the media library.
func (s *Service) Resolve(ctx context.Context, req ai.MediaResolveRequest) (ai.MediaResolveResult, bool) {
	if s == nil || !s.cfg.Enabled || s.repo == nil || req.GuildID == "" {
		return ai.MediaResolveResult{}, false
	}

	tags := ai.ParseMediaTags(req.RawReply)
	if len(tags) == 0 {
		return ai.MediaResolveResult{}, false
	}

	recentURLs := s.recentBotURLs(ctx, req.ChannelID, req.BotID)
	candidateLimit := s.candidateLimit()

	for _, tag := range tags {
		if tag.Description != "" {
			if result, ok := s.resolveDescribedTag(ctx, req.GuildID, tag, recentURLs, candidateLimit); ok {
				return result, true
			}
			continue
		}
		if tag.Kind == "gif" {
			if result, ok := s.resolveBareGifTag(ctx, req.GuildID, tag, req.TargetText, recentURLs, candidateLimit); ok {
				return result, true
			}
		}
	}

	return ai.MediaResolveResult{}, false
}

func (s *Service) candidateLimit() int {
	limit := s.cfg.RepeatWindow*2 + 20
	if limit < 50 {
		limit = 50
	}
	return limit
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

func (s *Service) recentBotURLs(ctx context.Context, channelID, botID string) map[string]bool {
	if s.cfg.RepeatWindow <= 0 || channelID == "" || botID == "" {
		return nil
	}
	messages, err := s.repo.GetRecentBotMessages(ctx, channelID, botID, s.cfg.RepeatWindow)
	if err != nil {
		logger.Warn("Media library failed to read recent bot messages for repeat window",
			zap.String("channel_id", channelID),
			zap.Error(err))
		return nil
	}
	if len(messages) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(messages))
	for _, content := range messages {
		for _, u := range ai.ExtractAllURLs(content) {
			seen[u] = true
		}
	}
	return seen
}

func (s *Service) resolveDescribedTag(ctx context.Context, guildID string, tag ai.MediaTag, recentURLs map[string]bool, limit int) (ai.MediaResolveResult, bool) {
	// 1. Exact description match in the same guild (ordered by uses DESC, last_seen DESC).
	exactRows, err := s.repo.FindExactDescription(ctx, guildID, tag.Kind, tag.Description, limit)
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
			hits, err := s.repo.SearchByDescription(ctx, guildID, tag.Kind, queryBlob, limit)
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
	if s.cfg.CrossGuild {
		crossExact, err := s.repo.FindExactDescriptionCrossGuild(ctx, guildID, tag.Kind, tag.Description, limit)
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

		if len(queryBlob) > 0 {
			crossHits, err := s.repo.SearchByDescriptionCrossGuild(ctx, guildID, tag.Kind, queryBlob, limit)
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

func (s *Service) resolveBareGifTag(ctx context.Context, guildID string, tag ai.MediaTag, targetText string, recentURLs map[string]bool, limit int) (ai.MediaResolveResult, bool) {
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
	hits, err := s.repo.SearchByTrigger(ctx, guildID, "gif", queryBlob, limit)
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

	if s.cfg.CrossGuild {
		crossHits, err := s.repo.SearchByTriggerCrossGuild(ctx, guildID, "gif", queryBlob, limit)
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

// RecordDiscordMessage indexes gif and described-link URLs from a human guild
// message, updating counts, timestamps, trigger texts, and embeddings.
func (s *Service) RecordDiscordMessage(ctx context.Context, msg discord.Message) error {
	if s == nil || !s.cfg.Enabled || s.repo == nil {
		return nil
	}
	if msg.GuildID == nil || msg.Author.Bot || msg.WebhookID != nil {
		return nil
	}

	items := ai.ExtractMediaURLs(ai.MessageFromDiscord(msg))
	if len(items) == 0 {
		return nil
	}

	guildID := msg.GuildID.String()
	msgTime := discordMessageTime(msg)
	ts := msgTime.Unix()
	newTrigger := s.resolveLiveTriggerText(ctx, msg, msgTime)

	for _, item := range items {
		existing, err := s.repo.GetEntry(ctx, item.URL, guildID)
		if err != nil {
			logger.Warn("Media library failed to load existing entry before live update",
				zap.String("url", item.URL),
				zap.String("guild_id", guildID),
				zap.Error(err))
		}

		effectiveDesc := item.Description
		existingTrigger := ""
		if existing != nil {
			if effectiveDesc == "" {
				effectiveDesc = existing.Description
			}
			existingTrigger = existing.TriggerText
		}
		mergedTrigger := prependTriggerText(existingTrigger, newTrigger, MaxTriggerTexts)

		var descEmb []byte
		if effectiveDesc != "" && s.embedder != nil &&
			(existing == nil || existing.Description != effectiveDesc || len(existing.DescriptionEmbedding) == 0) {
			descEmb, err = EmbedDocument(ctx, s.embedder, effectiveDesc)
			if err != nil {
				logger.Warn("Media library failed to compute description embedding",
					zap.String("url", item.URL),
					zap.String("guild_id", guildID),
					zap.Error(err))
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
			}
		}

		if err := s.repo.UpsertLive(ctx, Entry{
			GuildID:              guildID,
			URL:                  item.URL,
			Kind:                 item.Kind,
			Description:          item.Description,
			FirstSeen:            ts,
			LastSeen:             ts,
			TriggerText:          mergedTrigger,
			DescriptionEmbedding: descEmb,
			TriggerEmbedding:     trigEmb,
		}); err != nil {
			logger.Warn("Media library failed to upsert live entry",
				zap.String("url", item.URL),
				zap.String("guild_id", guildID),
				zap.Error(err))
		}
	}

	return nil
}

func (s *Service) resolveLiveTriggerText(ctx context.Context, msg discord.Message, msgTime time.Time) string {
	names := make(map[string]string)
	addNames := func(m discord.Message) {
		if m.Author.ID != 0 && m.Author.Username != "" {
			names[m.Author.ID.String()] = m.Author.Username
		}
		for _, u := range m.Mentions {
			if u.ID != 0 && u.Username != "" {
				names[u.ID.String()] = u.Username
			}
		}
	}
	addNames(msg)

	// 1. Replied-to message takes priority.
	if msg.MessageReference != nil && msg.MessageReference.MessageID != nil {
		refMsg := msg.ReferencedMessage
		if refMsg == nil {
			cached, err := s.repo.GetDiscordMessageByID(ctx, msg.MessageReference.MessageID.String())
			if err == nil {
				refMsg = cached
			}
		}
		if refMsg != nil {
			addNames(*refMsg)
			if rendered := strings.TrimSpace(ai.RenderCompletionTextV1(ai.MessageFromDiscord(*refMsg), names)); rendered != "" {
				return rendered
			}
		}
	}

	// 2. Previous human message in the same channel within 5 minutes.
	recent, err := s.repo.GetRecentDiscordMessagesByChannel(ctx, msg.ChannelID.String(), recentChannelScanLimit)
	if err != nil {
		return ""
	}
	for _, cand := range recent {
		addNames(cand)
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
		if rendered := strings.TrimSpace(ai.RenderCompletionTextV1(ai.MessageFromDiscord(cand), names)); rendered != "" {
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
