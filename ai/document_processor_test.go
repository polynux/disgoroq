package ai

import (
	"context"
	"testing"

	"polynux/disgoroq/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type documentCacheProviderStub struct {
	cache      database.AttachmentCache
	scopes     []AttachmentCacheScope
	chatCalled bool
}

func (p *documentCacheProviderStub) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	p.chatCalled = true
	return &ChatResponse{Content: "should not be used"}, nil
}

func (p *documentCacheProviderStub) Vision(ctx context.Context, req *VisionRequest) (*VisionResponse, error) {
	return nil, nil
}

func (p *documentCacheProviderStub) Name() string {
	return "stub"
}

func (p *documentCacheProviderStub) AvailableModels() []ModelInfo {
	return nil
}

func (p *documentCacheProviderStub) AttachmentCache() database.AttachmentCache {
	return p.cache
}

func (p *documentCacheProviderStub) ChatCacheScopes() []AttachmentCacheScope {
	return p.scopes
}

func TestDocumentProcessorUsesCachedSummaryBeforeDownload(t *testing.T) {
	cache := newMemoryAttachmentCache()
	doc := DocumentContext{
		URL:         "https://example.invalid/report.pdf",
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Size:        1234,
	}
	input := documentSummaryCacheInput(doc, 500)

	require.NoError(t, cache.PutAttachmentCache(context.Background(), database.AttachmentCacheEntry{
		AttachmentCacheKey: attachmentCacheKey(input, "groq", "llama-3.1-8b-instant"),
		Content:            "## Cached summary",
		SourceURL:          input.SourceURL,
		Filename:           input.Filename,
		ContentType:        input.ContentType,
		SizeBytes:          input.SizeBytes,
	}))

	provider := &documentCacheProviderStub{
		cache: cache,
		scopes: []AttachmentCacheScope{{
			Provider: "groq",
			Model:    "llama-3.1-8b-instant",
		}},
	}
	processor := NewDocumentProcessor(provider, DocumentProcessorConfig{
		MaxSummaryTokens: 500,
	})

	summary, err := processor.ProcessDocument(context.Background(), doc)

	require.NoError(t, err)
	assert.Equal(t, "## Cached summary", summary)
	assert.False(t, provider.chatCalled)
}
