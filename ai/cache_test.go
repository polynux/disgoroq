package ai

import (
	"context"

	"polynux/disgoroq/database"
)

type memoryAttachmentCache struct {
	entries map[database.AttachmentCacheKey]database.AttachmentCacheEntry
}

func newMemoryAttachmentCache() *memoryAttachmentCache {
	return &memoryAttachmentCache{
		entries: make(map[database.AttachmentCacheKey]database.AttachmentCacheEntry),
	}
}

func (c *memoryAttachmentCache) GetAttachmentCache(ctx context.Context, key database.AttachmentCacheKey) (database.AttachmentCacheEntry, bool, error) {
	entry, ok := c.entries[key]
	return entry, ok, nil
}

func (c *memoryAttachmentCache) PutAttachmentCache(ctx context.Context, entry database.AttachmentCacheEntry) error {
	c.entries[entry.AttachmentCacheKey] = entry
	return nil
}
