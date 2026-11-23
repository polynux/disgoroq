package medialibrary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"polynux/disgoroq/db"
)

// Entry represents one row in the media_library table.
type Entry struct {
	ID                   int64
	GuildID              string
	URL                  string
	Kind                 string
	Description          string
	Uses                 int64
	FirstSeen            int64
	LastSeen             int64
	TriggerText          string
	DescriptionEmbedding []byte
	TriggerEmbedding     []byte
}

// SearchHit pairs a media library entry with its cosine similarity score.
type SearchHit struct {
	Entry      Entry
	Similarity float64
}

// LookupFilter holds SQL-level filters applied before LIMIT in media lookups.
type LookupFilter struct {
	AllowDiscordCDN bool
	ExcludeURLs     []string
}

// Repository wraps sqlc queries for media_library and cached discord_messages.
type Repository struct {
	queries *db.Queries
}

// NewRepository creates a Repository backed by dbConn.
func NewRepository(dbConn *sql.DB) *Repository {
	return &Repository{queries: db.New(dbConn)}
}

func nullableBlob(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func allowDiscordCDNArg(allow bool) int64 {
	if allow {
		return 1
	}
	return 0
}

// encodeExcludeURLs builds a newline-framed list ("\nurl1\nurl2\n") for exact
// whole-URL matching via SQLite INSTR(?, char(10) || url || char(10)) = 0.
func encodeExcludeURLs(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	var b strings.Builder
	seen := make(map[string]bool, len(urls))
	for _, u := range urls {
		if u == "" || strings.ContainsRune(u, '\n') || seen[u] {
			continue
		}
		seen[u] = true
		if b.Len() == 0 {
			b.WriteByte('\n')
		}
		b.WriteString(u)
		b.WriteByte('\n')
	}
	return b.String()
}

func distanceToSimilarity(raw any) (float64, bool) {
	var dist float64
	switch d := raw.(type) {
	case float64:
		dist = d
	case float32:
		dist = float64(d)
	case int64:
		dist = float64(d)
	default:
		return 0, false
	}
	if math.IsNaN(dist) || math.IsInf(dist, 0) {
		return 0, false
	}
	return 1.0 - dist, true
}

func (r *Repository) GetEntry(ctx context.Context, rawURL, guildID string) (*Entry, error) {
	row, err := r.queries.GetMediaLibraryEntry(ctx, db.GetMediaLibraryEntryParams{
		Url:     rawURL,
		GuildID: guildID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	descEmb, err := rawBlobBytes(row.DescriptionEmbedding)
	if err != nil {
		return nil, fmt.Errorf("decode description_embedding: %w", err)
	}
	trigEmb, err := rawBlobBytes(row.TriggerEmbedding)
	if err != nil {
		return nil, fmt.Errorf("decode trigger_embedding: %w", err)
	}
	return &Entry{
		ID:                   row.ID,
		GuildID:              string(row.GuildID),
		URL:                  string(row.Url),
		Kind:                 string(row.Kind),
		Description:          string(row.Description),
		Uses:                 row.Uses,
		FirstSeen:            row.FirstSeen,
		LastSeen:             row.LastSeen,
		TriggerText:          string(row.TriggerText),
		DescriptionEmbedding: descEmb,
		TriggerEmbedding:     trigEmb,
	}, nil
}

func (r *Repository) UpsertImport(ctx context.Context, entry Entry) error {
	return r.queries.UpsertMediaLibraryImport(ctx, db.UpsertMediaLibraryImportParams{
		GuildID:              entry.GuildID,
		Url:                  entry.URL,
		Kind:                 entry.Kind,
		Description:          entry.Description,
		Uses:                 entry.Uses,
		FirstSeen:            entry.FirstSeen,
		LastSeen:             entry.LastSeen,
		TriggerText:          entry.TriggerText,
		DescriptionEmbedding: nullableBlob(entry.DescriptionEmbedding),
		TriggerEmbedding:     nullableBlob(entry.TriggerEmbedding),
	})
}

func (r *Repository) UpsertLive(ctx context.Context, entry Entry) error {
	return r.queries.UpsertMediaLibraryLive(ctx, db.UpsertMediaLibraryLiveParams{
		GuildID:              entry.GuildID,
		Url:                  entry.URL,
		Kind:                 entry.Kind,
		Description:          entry.Description,
		FirstSeen:            entry.FirstSeen,
		LastSeen:             entry.LastSeen,
		TriggerText:          entry.TriggerText,
		DescriptionEmbedding: nullableBlob(entry.DescriptionEmbedding),
		TriggerEmbedding:     nullableBlob(entry.TriggerEmbedding),
	})
}

func (r *Repository) FindExactDescription(ctx context.Context, guildID, kind, description string, filter LookupFilter, limit int) ([]Entry, error) {
	rows, err := r.queries.FindMediaLibraryExactDescription(ctx, db.FindMediaLibraryExactDescriptionParams{
		GuildID:         guildID,
		Kind:            kind,
		Description:     description,
		AllowDiscordCdn: allowDiscordCDNArg(filter.AllowDiscordCDN),
		ExcludeUrls:     encodeExcludeURLs(filter.ExcludeURLs),
		ResultLimit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(rows))
	for i, row := range rows {
		out[i] = Entry{
			ID:          row.ID,
			GuildID:     string(row.GuildID),
			URL:         string(row.Url),
			Kind:        string(row.Kind),
			Description: string(row.Description),
			Uses:        row.Uses,
			FirstSeen:   row.FirstSeen,
			LastSeen:    row.LastSeen,
			TriggerText: string(row.TriggerText),
		}
	}
	return out, nil
}

func (r *Repository) FindExactDescriptionCrossGuild(ctx context.Context, excludeGuildID, kind, description string, filter LookupFilter, limit int) ([]Entry, error) {
	rows, err := r.queries.FindMediaLibraryExactDescriptionCrossGuild(ctx, db.FindMediaLibraryExactDescriptionCrossGuildParams{
		ExcludeGuildID:  excludeGuildID,
		Kind:            kind,
		Description:     description,
		AllowDiscordCdn: allowDiscordCDNArg(filter.AllowDiscordCDN),
		ExcludeUrls:     encodeExcludeURLs(filter.ExcludeURLs),
		ResultLimit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(rows))
	for i, row := range rows {
		out[i] = Entry{
			ID:          row.ID,
			GuildID:     string(row.GuildID),
			URL:         string(row.Url),
			Kind:        string(row.Kind),
			Description: string(row.Description),
			Uses:        row.Uses,
			FirstSeen:   row.FirstSeen,
			LastSeen:    row.LastSeen,
			TriggerText: string(row.TriggerText),
		}
	}
	return out, nil
}

func (r *Repository) SearchByDescription(ctx context.Context, guildID, kind string, queryBlob []byte, filter LookupFilter, limit int) ([]SearchHit, error) {
	rows, err := r.queries.SearchMediaLibraryByDescription(ctx, db.SearchMediaLibraryByDescriptionParams{
		QueryEmbedding:  queryBlob,
		GuildID:         guildID,
		Kind:            kind,
		AllowDiscordCdn: allowDiscordCDNArg(filter.AllowDiscordCDN),
		ExcludeUrls:     encodeExcludeURLs(filter.ExcludeURLs),
		ResultLimit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		sim, ok := distanceToSimilarity(row.Distance)
		if !ok {
			continue
		}
		out = append(out, SearchHit{
			Entry: Entry{
				ID:          row.ID,
				GuildID:     string(row.GuildID),
				URL:         string(row.Url),
				Kind:        string(row.Kind),
				Description: string(row.Description),
				Uses:        row.Uses,
				FirstSeen:   row.FirstSeen,
				LastSeen:    row.LastSeen,
				TriggerText: string(row.TriggerText),
			},
			Similarity: sim,
		})
	}
	return out, nil
}

func (r *Repository) SearchByDescriptionCrossGuild(ctx context.Context, excludeGuildID, kind string, queryBlob []byte, filter LookupFilter, limit int) ([]SearchHit, error) {
	rows, err := r.queries.SearchMediaLibraryByDescriptionCrossGuild(ctx, db.SearchMediaLibraryByDescriptionCrossGuildParams{
		QueryEmbedding:  queryBlob,
		ExcludeGuildID:  excludeGuildID,
		Kind:            kind,
		AllowDiscordCdn: allowDiscordCDNArg(filter.AllowDiscordCDN),
		ExcludeUrls:     encodeExcludeURLs(filter.ExcludeURLs),
		ResultLimit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		sim, ok := distanceToSimilarity(row.Distance)
		if !ok {
			continue
		}
		out = append(out, SearchHit{
			Entry: Entry{
				ID:          row.ID,
				GuildID:     string(row.GuildID),
				URL:         string(row.Url),
				Kind:        string(row.Kind),
				Description: string(row.Description),
				Uses:        row.Uses,
				FirstSeen:   row.FirstSeen,
				LastSeen:    row.LastSeen,
				TriggerText: string(row.TriggerText),
			},
			Similarity: sim,
		})
	}
	return out, nil
}

func (r *Repository) SearchByTrigger(ctx context.Context, guildID, kind string, queryBlob []byte, filter LookupFilter, limit int) ([]SearchHit, error) {
	rows, err := r.queries.SearchMediaLibraryByTrigger(ctx, db.SearchMediaLibraryByTriggerParams{
		QueryEmbedding:  queryBlob,
		GuildID:         guildID,
		Kind:            kind,
		AllowDiscordCdn: allowDiscordCDNArg(filter.AllowDiscordCDN),
		ExcludeUrls:     encodeExcludeURLs(filter.ExcludeURLs),
		ResultLimit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		sim, ok := distanceToSimilarity(row.Distance)
		if !ok {
			continue
		}
		out = append(out, SearchHit{
			Entry: Entry{
				ID:          row.ID,
				GuildID:     string(row.GuildID),
				URL:         string(row.Url),
				Kind:        string(row.Kind),
				Description: string(row.Description),
				Uses:        row.Uses,
				FirstSeen:   row.FirstSeen,
				LastSeen:    row.LastSeen,
				TriggerText: string(row.TriggerText),
			},
			Similarity: sim,
		})
	}
	return out, nil
}

func (r *Repository) SearchByTriggerCrossGuild(ctx context.Context, excludeGuildID, kind string, queryBlob []byte, filter LookupFilter, limit int) ([]SearchHit, error) {
	rows, err := r.queries.SearchMediaLibraryByTriggerCrossGuild(ctx, db.SearchMediaLibraryByTriggerCrossGuildParams{
		QueryEmbedding:  queryBlob,
		ExcludeGuildID:  excludeGuildID,
		Kind:            kind,
		AllowDiscordCdn: allowDiscordCDNArg(filter.AllowDiscordCDN),
		ExcludeUrls:     encodeExcludeURLs(filter.ExcludeURLs),
		ResultLimit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		sim, ok := distanceToSimilarity(row.Distance)
		if !ok {
			continue
		}
		out = append(out, SearchHit{
			Entry: Entry{
				ID:          row.ID,
				GuildID:     string(row.GuildID),
				URL:         string(row.Url),
				Kind:        string(row.Kind),
				Description: string(row.Description),
				Uses:        row.Uses,
				FirstSeen:   row.FirstSeen,
				LastSeen:    row.LastSeen,
				TriggerText: string(row.TriggerText),
			},
			Similarity: sim,
		})
	}
	return out, nil
}

func (r *Repository) SearchAllKindsByDescription(ctx context.Context, guildID string, queryBlob []byte, limit int) ([]SearchHit, error) {
	rows, err := r.queries.SearchMediaLibraryAllKindsByDescription(ctx, db.SearchMediaLibraryAllKindsByDescriptionParams{
		Vector32: queryBlob,
		GuildID:  guildID,
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		sim, ok := distanceToSimilarity(row.Distance)
		if !ok {
			continue
		}
		out = append(out, SearchHit{
			Entry: Entry{
				ID:          row.ID,
				GuildID:     string(row.GuildID),
				URL:         string(row.Url),
				Kind:        string(row.Kind),
				Description: string(row.Description),
				Uses:        row.Uses,
				FirstSeen:   row.FirstSeen,
				LastSeen:    row.LastSeen,
				TriggerText: string(row.TriggerText),
			},
			Similarity: sim,
		})
	}
	return out, nil
}

func (r *Repository) SearchAllKindsByTrigger(ctx context.Context, guildID string, queryBlob []byte, limit int) ([]SearchHit, error) {
	rows, err := r.queries.SearchMediaLibraryAllKindsByTrigger(ctx, db.SearchMediaLibraryAllKindsByTriggerParams{
		Vector32: queryBlob,
		GuildID:  guildID,
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		sim, ok := distanceToSimilarity(row.Distance)
		if !ok {
			continue
		}
		out = append(out, SearchHit{
			Entry: Entry{
				ID:          row.ID,
				GuildID:     string(row.GuildID),
				URL:         string(row.Url),
				Kind:        string(row.Kind),
				Description: string(row.Description),
				Uses:        row.Uses,
				FirstSeen:   row.FirstSeen,
				LastSeen:    row.LastSeen,
				TriggerText: string(row.TriggerText),
			},
			Similarity: sim,
		})
	}
	return out, nil
}

func (r *Repository) GetRecentBotMessages(ctx context.Context, channelID, botID string, limit int) ([]string, error) {
	if limit <= 0 || channelID == "" || botID == "" {
		return nil, nil
	}
	rows, err := r.queries.GetRecentBotMessagesByChannel(ctx, db.GetRecentBotMessagesByChannelParams{
		ChannelID: channelID,
		AuthorID:  botID,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, raw := range rows {
		out[i] = string(raw)
	}
	return out, nil
}

func (r *Repository) GetDiscordMessageByID(ctx context.Context, messageID string) (*discord.Message, error) {
	row, err := r.queries.GetDiscordMessageByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var msg discord.Message
	if err := json.Unmarshal([]byte(row.MessageJson), &msg); err != nil {
		return nil, fmt.Errorf("unmarshal cached discord message %s: %w", row.MessageID, err)
	}
	return &msg, nil
}

func (r *Repository) GetRecentDiscordMessagesByChannel(ctx context.Context, channelID string, limit int) ([]discord.Message, error) {
	rows, err := r.queries.GetRecentDiscordMessagesByChannel(ctx, db.GetRecentDiscordMessagesByChannelParams{
		ChannelID: channelID,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	messages := make([]discord.Message, 0, len(rows))
	for _, row := range rows {
		var msg discord.Message
		if err := json.Unmarshal([]byte(row.MessageJson), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
	}
	return messages, nil
}
