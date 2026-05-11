package database

import (
	"context"
	"database/sql"
	"errors"

	"polynux/disgoroq/db"
)

type AttachmentCache interface {
	GetAttachmentCache(ctx context.Context, key AttachmentCacheKey) (AttachmentCacheEntry, bool, error)
	PutAttachmentCache(ctx context.Context, entry AttachmentCacheEntry) error
}

type AttachmentCacheKey struct {
	Kind               string
	AttachmentKey      string
	Provider           string
	Model              string
	InstructionVersion string
}

type AttachmentCacheEntry struct {
	AttachmentCacheKey
	Content     string
	SourceURL   string
	Filename    string
	ContentType string
	SizeBytes   int64
	CreatedAt   int64
	UpdatedAt   int64
}

func (r *Repository) GetAttachmentCache(ctx context.Context, key AttachmentCacheKey) (AttachmentCacheEntry, bool, error) {
	entry, err := r.queries.GetAttachmentCacheEntry(ctx, db.GetAttachmentCacheEntryParams{
		CacheKind:          key.Kind,
		AttachmentKey:      key.AttachmentKey,
		Provider:           key.Provider,
		Model:              key.Model,
		InstructionVersion: key.InstructionVersion,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AttachmentCacheEntry{}, false, nil
		}
		return AttachmentCacheEntry{}, false, err
	}

	return AttachmentCacheEntry{
		AttachmentCacheKey: AttachmentCacheKey{
			Kind:               entry.CacheKind,
			AttachmentKey:      entry.AttachmentKey,
			Provider:           entry.Provider,
			Model:              entry.Model,
			InstructionVersion: entry.InstructionVersion,
		},
		Content:     entry.Content,
		SourceURL:   entry.SourceUrl,
		Filename:    entry.Filename,
		ContentType: entry.ContentType,
		SizeBytes:   entry.SizeBytes,
		CreatedAt:   entry.CreatedAt,
		UpdatedAt:   entry.UpdatedAt,
	}, true, nil
}

func (r *Repository) PutAttachmentCache(ctx context.Context, entry AttachmentCacheEntry) error {
	return r.queries.UpsertAttachmentCacheEntry(ctx, db.UpsertAttachmentCacheEntryParams{
		CacheKind:          entry.Kind,
		AttachmentKey:      entry.AttachmentKey,
		SourceUrl:          entry.SourceURL,
		Filename:           entry.Filename,
		ContentType:        entry.ContentType,
		SizeBytes:          entry.SizeBytes,
		Provider:           entry.Provider,
		Model:              entry.Model,
		InstructionVersion: entry.InstructionVersion,
		Content:            entry.Content,
	})
}
