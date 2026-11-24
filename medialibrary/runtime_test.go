package medialibrary

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
)

type hookEmbedder struct {
	vec    []float32
	onCall func()
}

func (h *hookEmbedder) GenerateEmbedding(_ context.Context, _ string) ([]float32, error) {
	if h.onCall != nil {
		h.onCall()
	}
	return h.vec, nil
}

func TestResolveUsesSingleConfigSnapshotAndPinsEnabled(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	// Seed one gif entry whose description vector has 0.80 cosine similarity to the query.
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "guild-1",
		URL:                  "https://tenor.com/view/cat-dance-123",
		Kind:                 "gif",
		Description:          "cat dancing",
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.80)),
		FirstSeen:            1000,
		LastSeen:             1000,
		Uses:                 1,
	}))

	// 1. Single snapshot per Resolve call:
	// Start with MinSimilarity=0.50 (matches 0.80), and mutate Runtime to MinSimilarity=0.99
	// inside the embedder callback mid-Resolve.
	initial := config.DefaultConfig()
	initial.MediaLibrary.Enabled = true
	initial.MediaLibrary.MinSimilarity = 0.50
	runtime := config.NewRuntime(initial)

	var cfgReads atomic.Int32
	var mutated atomic.Bool
	embedder := &hookEmbedder{
		vec: unitVec2D(1, 0),
		onCall: func() {
			if mutated.CompareAndSwap(false, true) {
				next := config.DefaultConfig()
				next.MediaLibrary.Enabled = true
				next.MediaLibrary.MinSimilarity = 0.99
				runtime.Store(next)
			}
		},
	}

	svc := NewService(repo, embedder, initial.MediaLibrary)
	svc.SetConfigSource(func() config.MediaLibraryConfig {
		cfgReads.Add(1)
		return runtime.Current().MediaLibrary
	})

	req := ai.MediaResolveRequest{
		GuildID:  "guild-1",
		RawReply: "[gif: happy cat]",
	}

	// First Resolve must use the initial snapshot (MinSimilarity=0.50) despite mid-call mutation.
	res, ok := svc.Resolve(ctx, req)
	require.True(t, ok, "in-flight Resolve should use the snapshot captured at call start (0.50)")
	assert.Equal(t, "https://tenor.com/view/cat-dance-123", res.URL)
	assert.Equal(t, int32(1), cfgReads.Load(), "Resolve must read the config source exactly once")

	// Second Resolve picks up the new snapshot (MinSimilarity=0.99) and rejects the 0.80 hit.
	_, ok = svc.Resolve(ctx, req)
	assert.False(t, ok, "subsequent Resolve should use updated MinSimilarity=0.99")
	assert.Equal(t, int32(2), cfgReads.Load())

	// 2. Enabled stays pinned to startup value:
	// 2a. Service started enabled stays enabled even if runtime flips Enabled=false.
	disabledInRuntime := config.DefaultConfig()
	disabledInRuntime.MediaLibrary.Enabled = false
	disabledInRuntime.MediaLibrary.MinSimilarity = 0.50
	runtime.Store(disabledInRuntime)

	res, ok = svc.Resolve(ctx, req)
	require.True(t, ok, "startup-enabled service must ignore runtime Enabled=false")
	assert.Equal(t, "https://tenor.com/view/cat-dance-123", res.URL)

	// 2b. Service started disabled stays disabled even if runtime flips Enabled=true.
	enabledInRuntime := config.DefaultConfig()
	enabledInRuntime.MediaLibrary.Enabled = true
	enabledInRuntime.MediaLibrary.MinSimilarity = 0.50
	runtime.Store(enabledInRuntime)

	disabledSvc := NewService(repo, embedder, config.MediaLibraryConfig{Enabled: false, MinSimilarity: 0.50})
	disabledSvc.SetConfigSource(func() config.MediaLibraryConfig {
		return runtime.Current().MediaLibrary
	})
	_, ok = disabledSvc.Resolve(ctx, req)
	assert.False(t, ok, "startup-disabled service must ignore runtime Enabled=true")
}
