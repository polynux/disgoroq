package medialibrary

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/logger"
)

// MaxConsecutiveImportEmbedErrors is the threshold of consecutive embedding
// failures after which ImportJSONL aborts with an error.
const MaxConsecutiveImportEmbedErrors = 20

// ImportRecord matches one JSONL line for -import-media-library.
type ImportRecord struct {
	URL          string   `json:"url"`
	Kind         string   `json:"kind"`
	Description  string   `json:"description"`
	GuildID      string   `json:"guild_id"`
	Uses         int64    `json:"uses"`
	FirstSeen    string   `json:"first_seen"`
	LastSeen     string   `json:"last_seen"`
	TriggerTexts []string `json:"trigger_texts"`
}

// ImportSummary summarizes the result of a JSONL import run.
type ImportSummary struct {
	Rows               int
	EmbeddingsComputed int
	EmbeddingsReused   int
	Skipped            int
}

// ImportFile imports a JSONL file into media_library.
func (s *Service) ImportFile(ctx context.Context, path string, progress func(ImportSummary)) (ImportSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return ImportSummary{}, fmt.Errorf("open media library import file: %w", err)
	}
	defer f.Close()
	return s.ImportJSONL(ctx, f, progress)
}

// ImportJSONL imports JSONL records into media_library idempotently and resumably.
// Re-importing overwrites counts, dates, and triggers, while reusing existing
// embeddings whenever the stored text is unchanged. It aborts after
// MaxConsecutiveImportEmbedErrors consecutive embedding failures.
func (s *Service) ImportJSONL(ctx context.Context, r io.Reader, progress func(ImportSummary)) (ImportSummary, error) {
	if s == nil || s.repo == nil {
		return ImportSummary{}, fmt.Errorf("media library repository is not initialized")
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var summary ImportSummary
	lineNum := 0
	consecutiveEmbedErrors := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec ImportRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			logger.Warn("Skipping invalid JSONL line during media library import",
				zap.Int("line", lineNum),
				zap.Error(err))
			summary.Skipped++
			continue
		}

		canonicalURL := ai.CanonicalizeMediaURL(strings.TrimSpace(rec.URL))
		guildID := strings.TrimSpace(rec.GuildID)
		kind := strings.ToLower(strings.TrimSpace(rec.Kind))
		if canonicalURL == "" || guildID == "" || kind == "" {
			logger.Warn("Skipping incomplete media library record",
				zap.Int("line", lineNum),
				zap.String("url", rec.URL),
				zap.String("guild_id", rec.GuildID),
				zap.String("kind", rec.Kind))
			summary.Skipped++
			continue
		}

		firstSeen, err := parseImportTimestamp(rec.FirstSeen)
		if err != nil {
			logger.Warn("Skipping media library record with invalid first_seen",
				zap.Int("line", lineNum),
				zap.String("first_seen", rec.FirstSeen),
				zap.Error(err))
			summary.Skipped++
			continue
		}
		lastSeen, err := parseImportTimestamp(rec.LastSeen)
		if err != nil {
			logger.Warn("Skipping media library record with invalid last_seen",
				zap.Int("line", lineNum),
				zap.String("last_seen", rec.LastSeen),
				zap.Error(err))
			summary.Skipped++
			continue
		}
		if firstSeen == 0 && lastSeen == 0 {
			now := time.Now().Unix()
			firstSeen = now
			lastSeen = now
		} else if firstSeen == 0 {
			firstSeen = lastSeen
		} else if lastSeen == 0 {
			lastSeen = firstSeen
		}

		description := strings.TrimSpace(rec.Description)
		triggerText := joinTriggerTexts(rec.TriggerTexts, MaxTriggerTexts)

		existing, err := s.repo.GetEntry(ctx, canonicalURL, guildID)
		if err != nil {
			return summary, fmt.Errorf("line %d: load existing entry: %w", lineNum, err)
		}

		var descEmb []byte
		if description != "" {
			if existing != nil && existing.Description == description && len(existing.DescriptionEmbedding) > 0 {
				descEmb = existing.DescriptionEmbedding
				summary.EmbeddingsReused++
			} else if s.embedder != nil {
				descEmb, err = EmbedDocument(ctx, s.embedder, description)
				if err != nil {
					consecutiveEmbedErrors++
					logger.Warn("Failed to compute description embedding during import",
						zap.Int("line", lineNum),
						zap.String("url", canonicalURL),
						zap.Int("consecutive_failures", consecutiveEmbedErrors),
						zap.Error(err))
					if consecutiveEmbedErrors >= MaxConsecutiveImportEmbedErrors {
						return summary, fmt.Errorf("aborting import after %d consecutive embedding failures: %w", consecutiveEmbedErrors, err)
					}
				} else {
					consecutiveEmbedErrors = 0
					if len(descEmb) > 0 {
						summary.EmbeddingsComputed++
					}
				}
			}
		}

		var trigEmb []byte
		if triggerText != "" {
			if existing != nil && existing.TriggerText == triggerText && len(existing.TriggerEmbedding) > 0 {
				trigEmb = existing.TriggerEmbedding
				summary.EmbeddingsReused++
			} else if s.embedder != nil {
				trigEmb, err = EmbedDocument(ctx, s.embedder, triggerText)
				if err != nil {
					consecutiveEmbedErrors++
					logger.Warn("Failed to compute trigger embedding during import",
						zap.Int("line", lineNum),
						zap.String("url", canonicalURL),
						zap.Int("consecutive_failures", consecutiveEmbedErrors),
						zap.Error(err))
					if consecutiveEmbedErrors >= MaxConsecutiveImportEmbedErrors {
						return summary, fmt.Errorf("aborting import after %d consecutive embedding failures: %w", consecutiveEmbedErrors, err)
					}
				} else {
					consecutiveEmbedErrors = 0
					if len(trigEmb) > 0 {
						summary.EmbeddingsComputed++
					}
				}
			}
		}

		uses := rec.Uses
		if uses < 0 {
			uses = 0
		}

		if err := s.repo.UpsertImport(ctx, Entry{
			GuildID:              guildID,
			URL:                  canonicalURL,
			Kind:                 kind,
			Description:          description,
			Uses:                 uses,
			FirstSeen:            firstSeen,
			LastSeen:             lastSeen,
			TriggerText:          triggerText,
			DescriptionEmbedding: descEmb,
			TriggerEmbedding:     trigEmb,
		}); err != nil {
			return summary, fmt.Errorf("line %d: upsert media library entry: %w", lineNum, err)
		}

		summary.Rows++
		if progress != nil && summary.Rows%100 == 0 {
			progress(summary)
		}
	}

	if err := scanner.Err(); err != nil {
		return summary, fmt.Errorf("read media library import: %w", err)
	}
	return summary, nil
}

// joinTriggerTexts keeps the most recent maxItems non-empty unique trigger texts
// from a JSONL array ordered oldest -> newest (matching the exporter's [-3:]) and
// stores them newest first, the order prependTriggerText keeps for live updates.
// normalizeTriggerText canonicalises trigger text coming from the admin editor
// or an import: CRLF becomes LF, each line is trimmed, empty and duplicate
// lines are dropped, and the remaining lines keep their order.
func normalizeTriggerText(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func joinTriggerTexts(triggers []string, maxItems int) string {
	if len(triggers) == 0 || maxItems <= 0 {
		return ""
	}
	reversed := make([]string, 0, min(len(triggers), maxItems))
	seen := make(map[string]bool, maxItems)
	for i := len(triggers) - 1; i >= 0; i-- {
		cleaned := strings.TrimSpace(strings.ReplaceAll(triggers[i], "\n", " "))
		if cleaned == "" || seen[cleaned] {
			continue
		}
		reversed = append(reversed, cleaned)
		seen[cleaned] = true
		if len(reversed) >= maxItems {
			break
		}
	}
	return strings.Join(reversed, "\n")
}

func parseImportTimestamp(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.Unix(), nil
		}
	}
	if unixSec, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return unixSec, nil
	}
	return 0, fmt.Errorf("unsupported timestamp format %q", raw)
}
