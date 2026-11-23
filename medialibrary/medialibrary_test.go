package medialibrary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/db"
)

func setupMediaTestDB(t *testing.T) (*sql.DB, *Repository) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	conn, err := sql.Open("libsql", "file:"+dbPath)
	require.NoError(t, err)

	schemaBytes, err := os.ReadFile(filepath.Join("..", "schema.sql"))
	require.NoError(t, err)

	for _, stmt := range strings.Split(string(schemaBytes), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.Contains(stmt, "libsql_vector_idx") {
			continue
		}
		_, err := conn.Exec(stmt)
		require.NoError(t, err, "statement failed: %s", stmt)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return conn, NewRepository(conn)
}

type fakeEmbedder struct {
	mu      sync.Mutex
	vectors map[string][]float32
	errs    map[string]error
	calls   []string
}

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{
		vectors: make(map[string][]float32),
		errs:    make(map[string]error),
	}
}

func (f *fakeEmbedder) set(text string, vec []float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vectors[text] = vec
}

func (f *fakeEmbedder) setErr(text string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.errs, text)
		return
	}
	f.errs[text] = err
}

func (f *fakeEmbedder) callHistory() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeEmbedder) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeEmbedder) GenerateEmbedding(_ context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, text)
	if err, ok := f.errs[text]; ok {
		return nil, err
	}
	if vec, ok := f.vectors[text]; ok {
		return vec, nil
	}
	return unitVec2D(1, 0), nil
}

// unitVec2D returns a 768-dim unit vector in the first two coordinates so
// cosine similarity between unitVec2D(x1, y1) and unitVec2D(x2, y2) is exact.
func unitVec2D(x, y float64) []float32 {
	norm := math.Hypot(x, y)
	vec := make([]float32, EmbeddingDim)
	if norm == 0 {
		vec[0] = 1
		return vec
	}
	vec[0] = float32(x / norm)
	vec[1] = float32(y / norm)
	return vec
}

// vecWithSimilarityToXAxis builds a unit vector whose cosine similarity with
// unitVec2D(1, 0) equals sim (for 0 <= sim <= 1).
func vecWithSimilarityToXAxis(sim float64) []float32 {
	y := math.Sqrt(math.Max(0, 1-sim*sim))
	return unitVec2D(sim, y)
}

func mustBlob(t *testing.T, vec []float32) []byte {
	t.Helper()
	b, err := EncodeF32Blob(vec)
	require.NoError(t, err)
	return b
}

func defaultEnabledConfig() config.MediaLibraryConfig {
	return config.MediaLibraryConfig{
		Enabled:              true,
		MinSimilarity:        0.80,
		MinTriggerSimilarity: 0.80,
		CrossGuild:           false,
		AllowDiscordCDN:      false,
		RepeatWindow:         20,
	}
}

func insertCachedMessage(t *testing.T, conn *sql.DB, msg discord.Message) {
	t.Helper()
	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	guildID := ""
	if msg.GuildID != nil {
		guildID = msg.GuildID.String()
	}
	q := db.New(conn)
	err = q.UpsertDiscordMessage(context.Background(), db.UpsertDiscordMessageParams{
		MessageID:   msg.ID.String(),
		ChannelID:   msg.ChannelID.String(),
		GuildID:     guildID,
		AuthorID:    msg.Author.ID.String(),
		Content:     msg.Content,
		MessageJson: string(raw),
		CreatedAt:   msg.CreatedAt.Unix(),
	})
	require.NoError(t, err)
}

func TestEncodeDecodeF32Blob(t *testing.T) {
	vec := unitVec2D(3, 4)
	blob, err := EncodeF32Blob(vec)
	require.NoError(t, err)
	require.Len(t, blob, EmbeddingDim*4)

	decoded, err := DecodeF32Blob(blob)
	require.NoError(t, err)
	require.Len(t, decoded, EmbeddingDim)
	assert.InDelta(t, vec[0], decoded[0], 1e-6)
	assert.InDelta(t, vec[1], decoded[1], 1e-6)

	_, err = EncodeF32Blob([]float32{1, 2, 3})
	require.Error(t, err)

	bad := make([]float32, EmbeddingDim)
	bad[5] = float32(math.NaN())
	_, err = EncodeF32Blob(bad)
	require.Error(t, err)
}

func TestResolveExactMatchPriority(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	// Insert three exact-description matches with different uses and last_seen.
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/cat-smile-1",
		Kind:                 "gif",
		Description:          "cat smile",
		Uses:                 3,
		FirstSeen:            100,
		LastSeen:             300,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.5)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/cat-smile-2",
		Kind:                 "gif",
		Description:          "Cat Smile",
		Uses:                 5,
		FirstSeen:            100,
		LastSeen:             200,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.5)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/cat-smile-3",
		Kind:                 "gif",
		Description:          "CAT SMILE",
		Uses:                 5,
		FirstSeen:            100,
		LastSeen:             400,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.5)),
	}))

	embedder := newFakeEmbedder()
	svc := NewService(repo, embedder, defaultEnabledConfig())

	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "haha [gif: cat smile]",
	})
	require.True(t, ok)
	// Tie on uses=5 broken by most recent last_seen=400.
	assert.Equal(t, "https://tenor.com/view/cat-smile-3", res.URL)
	assert.Equal(t, "[gif: cat smile]", res.Tag)
	assert.Equal(t, 1.0, res.Similarity)
	// Exact match should not need to call the embedder.
	assert.Empty(t, embedder.callHistory())
}

func TestResolveSimilarityThresholdAndNomicPrefixes(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/happy-cat-1",
		Kind:                 "gif",
		Description:          "happy cat",
		Uses:                 2,
		FirstSeen:            100,
		LastSeen:             100,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.85)),
		TriggerText:          "good morning",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.84)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/sad-dog-2",
		Kind:                 "gif",
		Description:          "sad dog",
		Uses:                 10,
		FirstSeen:            100,
		LastSeen:             200,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.60)),
		TriggerText:          "bad day",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.55)),
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: smiling kitty", unitVec2D(1, 0))
	unrelatedVec := make([]float32, EmbeddingDim)
	unrelatedVec[2] = 1
	embedder.set("search_query: unrelated", unrelatedVec)
	embedder.set("search_query: hey good morning everyone", unitVec2D(1, 0))

	cfg := defaultEnabledConfig()
	cfg.MinSimilarity = 0.80
	cfg.MinTriggerSimilarity = 0.80
	svc := NewService(repo, embedder, cfg)

	// Description similarity 0.85 >= 0.80 -> match.
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: smiling kitty]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/happy-cat-1", res.URL)
	assert.InDelta(t, 0.85, res.Similarity, 1e-3)

	// Below threshold -> no match.
	_, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: unrelated]",
	})
	assert.False(t, ok)

	// Bare [gif] matches trigger_embedding >= 0.80.
	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:    "g1",
		RawReply:   "salut [gif]",
		TargetText: "hey good morning everyone",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/happy-cat-1", res.URL)
	assert.Equal(t, "[gif]", res.Tag)
	assert.InDelta(t, 0.84, res.Similarity, 1e-3)

	// Bare [gif] below min_trigger_similarity -> no match.
	cfgStrict := cfg
	cfgStrict.MinTriggerSimilarity = 0.90
	strictSvc := NewService(repo, embedder, cfgStrict)
	_, ok = strictSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:    "g1",
		RawReply:   "[gif]",
		TargetText: "hey good morning everyone",
	})
	assert.False(t, ok)

	// Verify nomic search_query prefix was used on every query call.
	for _, call := range embedder.callHistory() {
		assert.True(t, strings.HasPrefix(call, SearchQueryPrefix), "expected search_query prefix, got %q", call)
	}
}

func TestResolveKindCompatibility(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	sameVec := mustBlob(t, unitVec2D(1, 0))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/funny-clip-1",
		Kind:                 "gif",
		Description:          "funny clip",
		Uses:                 20,
		FirstSeen:            100,
		LastSeen:             200,
		DescriptionEmbedding: sameVec,
		TriggerText:          "lol",
		TriggerEmbedding:     sameVec,
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://www.youtube.com/watch?v=abc123",
		Kind:                 "youtube",
		Description:          "funny clip",
		Uses:                 1,
		FirstSeen:            100,
		LastSeen:             100,
		DescriptionEmbedding: sameVec,
		TriggerText:          "watch this",
		TriggerEmbedding:     sameVec,
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: funny clip", unitVec2D(1, 0))
	embedder.set("search_query: funny video", unitVec2D(1, 0))
	embedder.set("search_query: watch this", unitVec2D(1, 0))
	svc := NewService(repo, embedder, defaultEnabledConfig())

	// [youtube: funny clip] must resolve to youtube, never gif.
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[youtube: funny clip]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://www.youtube.com/watch?v=abc123", res.URL)

	// [tweet: funny video] has no tweet entries -> must not match gif or youtube.
	_, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[tweet: funny video]",
	})
	assert.False(t, ok)

	// Bare [gif] must match gif trigger, not youtube trigger (even if youtube has trigger).
	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:    "g1",
		RawReply:   "[gif]",
		TargetText: "watch this",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/funny-clip-1", res.URL)
}

func TestResolveCrossGuildFallback(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g2",
		URL:                  "https://tenor.com/view/other-guild-cat-1",
		Kind:                 "gif",
		Description:          "other guild cat",
		Uses:                 4,
		FirstSeen:            100,
		LastSeen:             200,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.92)),
		TriggerText:          "hello from g2",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.91)),
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: cat from elsewhere", unitVec2D(1, 0))
	embedder.set("search_query: hello", unitVec2D(1, 0))

	// Disabled cross_guild: no match in g1.
	localSvc := NewService(repo, embedder, defaultEnabledConfig())
	_, ok := localSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: other guild cat]",
	})
	assert.False(t, ok)

	// Enabled cross_guild: falls back to g2 for exact, vector, and bare trigger.
	cfg := defaultEnabledConfig()
	cfg.CrossGuild = true
	crossSvc := NewService(repo, embedder, cfg)

	res, ok := crossSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: other guild cat]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/other-guild-cat-1", res.URL)

	res, ok = crossSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: cat from elsewhere]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/other-guild-cat-1", res.URL)

	res, ok = crossSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:    "g1",
		RawReply:   "[gif]",
		TargetText: "hello",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/other-guild-cat-1", res.URL)
}

func TestResolveCDNExclusion(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://cdn.discordapp.com/attachments/1/2/cat.gif",
		Kind:                 "gif",
		Description:          "cat",
		Uses:                 50,
		FirstSeen:            100,
		LastSeen:             300,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.99)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://media.discordapp.net/attachments/1/2/cat2.gif",
		Kind:                 "gif",
		Description:          "cat",
		Uses:                 40,
		FirstSeen:            100,
		LastSeen:             250,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.95)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/cat-safe-3",
		Kind:                 "gif",
		Description:          "cat",
		Uses:                 2,
		FirstSeen:            100,
		LastSeen:             200,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.88)),
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: kitty", unitVec2D(1, 0))

	// Default allow_discord_cdn=false skips both Discord CDN hosts and picks Tenor.
	svc := NewService(repo, embedder, defaultEnabledConfig())
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/cat-safe-3", res.URL)

	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: kitty]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/cat-safe-3", res.URL)

	// allow_discord_cdn=true allows the highest-use CDN entry.
	cfgAllow := defaultEnabledConfig()
	cfgAllow.AllowDiscordCDN = true
	allowSvc := NewService(repo, embedder, cfgAllow)
	res, ok = allowSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://cdn.discordapp.com/attachments/1/2/cat.gif", res.URL)
}

func TestResolveRepeatWindow(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()

	gID := snowflake.ID(1000)
	ch1 := snowflake.ID(2000)
	ch2 := snowflake.ID(2001)
	botID := snowflake.ID(9000)
	humanID := snowflake.ID(3000)
	baseTime := time.Unix(1700000000, 0)

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:     gID.String(),
		URL:         "https://tenor.com/view/top-cat-1",
		Kind:        "gif",
		Description: "cat",
		Uses:        20,
		FirstSeen:   100,
		LastSeen:    300,
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:     gID.String(),
		URL:         "https://tenor.com/view/second-cat-2",
		Kind:        "gif",
		Description: "cat",
		Uses:        10,
		FirstSeen:   100,
		LastSeen:    200,
	}))

	// Bot posted top-cat-1 in ch1 2 bot messages ago.
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(101),
		GuildID:   &gID,
		ChannelID: ch1,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   "voila\nhttps://tenor.com/view/top-cat-1",
		CreatedAt: baseTime,
	})
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(102),
		GuildID:   &gID,
		ChannelID: ch1,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   "autre message",
		CreatedAt: baseTime.Add(time.Minute),
	})
	// Human posted second-cat-2 in ch1 -> should NOT block the bot.
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(103),
		GuildID:   &gID,
		ChannelID: ch1,
		Author:    discord.User{ID: humanID, Username: "alice"},
		Content:   "https://tenor.com/view/second-cat-2",
		CreatedAt: baseTime.Add(2 * time.Minute),
	})

	cfg := defaultEnabledConfig()
	cfg.RepeatWindow = 2
	svc := NewService(repo, newFakeEmbedder(), cfg)

	// Within repeat_window=2 in ch1: top-cat-1 is skipped, second-cat-2 is chosen.
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: ch1.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/second-cat-2", res.URL)

	// In ch2 (different channel): top-cat-1 is still eligible.
	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: ch2.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/top-cat-1", res.URL)

	// Once bot posts one more message in ch1, top-cat-1 falls outside repeat_window=2.
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(104),
		GuildID:   &gID,
		ChannelID: ch1,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   "encore un message",
		CreatedAt: baseTime.Add(3 * time.Minute),
	})
	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: ch1.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/top-cat-1", res.URL)
}

func TestResolveMultipleTagsReturnsAtMostOne(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:     "g1",
		URL:         "https://tenor.com/view/cat-smile-1",
		Kind:        "gif",
		Description: "cat smile",
		Uses:        3,
		FirstSeen:   100,
		LastSeen:    100,
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:     "g1",
		URL:         "https://www.youtube.com/watch?v=xyz",
		Kind:        "youtube",
		Description: "clip",
		Uses:        3,
		FirstSeen:   100,
		LastSeen:    100,
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: missing", unitVec2D(0, 1))
	svc := NewService(repo, embedder, defaultEnabledConfig())

	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: missing] [gif: cat smile] [youtube: clip]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/cat-smile-1", res.URL)
	assert.Equal(t, "[gif: cat smile]", res.Tag)
}

func TestRecordDiscordMessageLiveUpdates(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()

	embedder := newFakeEmbedder()
	svc := NewService(repo, embedder, defaultEnabledConfig())

	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	aliceID := snowflake.ID(3001)
	bobID := snowflake.ID(3002)
	botID := snowflake.ID(9000)
	webhookID := snowflake.ID(8000)
	t0 := time.Unix(1700001000, 0)

	// Cache a previous human message 2 minutes earlier, and a bot message 1 minute earlier.
	prevHuman := discord.Message{
		ID:        snowflake.ID(500),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "t'as vu ca ?",
		CreatedAt: t0.Add(-2 * time.Minute),
	}
	insertCachedMessage(t, conn, prevHuman)
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(501),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   "bot noise",
		CreatedAt: t0.Add(-1 * time.Minute),
	})

	// 1. Human posts a Tenor GIF + YouTube embed + Discord CDN GIF with query params.
	msg1 := discord.Message{
		ID:        snowflake.ID(502),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: bobID, Username: "bob"},
		Content:   "regarde https://tenor.com/view/cat-dance-12345. https://www.youtube.com/watch?v=abc https://cdn.discordapp.com/attachments/1/2/123456.gif?ex=1&hm=2",
		Embeds: []discord.Embed{
			{
				URL:   "https://www.youtube.com/watch?v=abc",
				Title: "Best Cat Video",
			},
		},
		CreatedAt: t0,
	}
	insertCachedMessage(t, conn, msg1)
	require.NoError(t, svc.RecordDiscordMessage(ctx, msg1))

	tenorEntry, err := repo.GetEntry(ctx, "https://tenor.com/view/cat-dance-12345", gID.String())
	require.NoError(t, err)
	require.NotNil(t, tenorEntry)
	assert.Equal(t, "gif", tenorEntry.Kind)
	assert.Equal(t, "cat dance", tenorEntry.Description)
	assert.Equal(t, int64(1), tenorEntry.Uses)
	assert.Equal(t, t0.Unix(), tenorEntry.FirstSeen)
	assert.Equal(t, t0.Unix(), tenorEntry.LastSeen)
	assert.Equal(t, "t'as vu ca ?", tenorEntry.TriggerText)
	assert.Len(t, tenorEntry.DescriptionEmbedding, EmbeddingDim*4)
	assert.Len(t, tenorEntry.TriggerEmbedding, EmbeddingDim*4)

	ytEntry, err := repo.GetEntry(ctx, "https://www.youtube.com/watch?v=abc", gID.String())
	require.NoError(t, err)
	require.NotNil(t, ytEntry)
	assert.Equal(t, "youtube", ytEntry.Kind)
	assert.Equal(t, "Best Cat Video", ytEntry.Description)

	cdnEntry, err := repo.GetEntry(ctx, "https://cdn.discordapp.com/attachments/1/2/123456.gif", gID.String())
	require.NoError(t, err)
	require.NotNil(t, cdnEntry)
	assert.Equal(t, "gif", cdnEntry.Kind)
	assert.Equal(t, "", cdnEntry.Description)
	assert.Empty(t, cdnEntry.DescriptionEmbedding)
	assert.Len(t, cdnEntry.TriggerEmbedding, EmbeddingDim*4)

	// 2. Reply to a specific older message takes priority over previous channel message,
	// increments uses, updates last_seen, and prepends trigger text (capped at 3).
	replyTarget := discord.Message{
		ID:        snowflake.ID(400),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "trigger two",
		CreatedAt: t0.Add(-30 * time.Minute),
	}
	insertCachedMessage(t, conn, replyTarget)

	for i, trig := range []string{"trigger two", "trigger three", "trigger four"} {
		refID := snowflake.ID(600 + i)
		ref := discord.Message{
			ID:        refID,
			GuildID:   &gID,
			ChannelID: chID,
			Author:    discord.User{ID: aliceID, Username: "alice"},
			Content:   trig,
			CreatedAt: t0.Add(time.Duration(i+1) * time.Minute),
		}
		insertCachedMessage(t, conn, ref)
		repost := discord.Message{
			ID:               snowflake.ID(700 + i),
			GuildID:          &gID,
			ChannelID:        chID,
			Author:           discord.User{ID: bobID, Username: "bob"},
			Content:          "https://tenor.com/view/cat-dance-12345",
			MessageReference: &discord.MessageReference{MessageID: &refID},
			CreatedAt:        t0.Add(time.Duration(i+10) * time.Minute),
		}
		require.NoError(t, svc.RecordDiscordMessage(ctx, repost))
	}

	updated, err := repo.GetEntry(ctx, "https://tenor.com/view/cat-dance-12345", gID.String())
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, int64(4), updated.Uses)
	assert.Equal(t, t0.Unix(), updated.FirstSeen)
	assert.Equal(t, t0.Add(12*time.Minute).Unix(), updated.LastSeen)
	assert.Equal(t, "trigger four\ntrigger three\ntrigger two", updated.TriggerText)

	// 3. Bots, webhooks, and disabled config are ignored.
	require.NoError(t, svc.RecordDiscordMessage(ctx, discord.Message{
		ID:        snowflake.ID(801),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   "https://tenor.com/view/bot-only-999",
		CreatedAt: t0.Add(20 * time.Minute),
	}))
	require.NoError(t, svc.RecordDiscordMessage(ctx, discord.Message{
		ID:        snowflake.ID(802),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "hook"},
		WebhookID: &webhookID,
		Content:   "https://tenor.com/view/webhook-only-999",
		CreatedAt: t0.Add(20 * time.Minute),
	}))
	disabledCfg := defaultEnabledConfig()
	disabledCfg.Enabled = false
	disabledSvc := NewService(repo, embedder, disabledCfg)
	require.NoError(t, disabledSvc.RecordDiscordMessage(ctx, discord.Message{
		ID:        snowflake.ID(803),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "https://tenor.com/view/disabled-only-999",
		CreatedAt: t0.Add(20 * time.Minute),
	}))

	for _, u := range []string{
		"https://tenor.com/view/bot-only-999",
		"https://tenor.com/view/webhook-only-999",
		"https://tenor.com/view/disabled-only-999",
	} {
		entry, err := repo.GetEntry(ctx, u, gID.String())
		require.NoError(t, err)
		assert.Nil(t, entry, "expected %s not to be indexed", u)
	}

	// Verify document prefix used on all live embedding calls.
	for _, call := range embedder.callHistory() {
		assert.True(t, strings.HasPrefix(call, SearchDocumentPrefix), "expected search_document prefix, got %q", call)
	}
}

func TestImportJSONLIdempotencyAndResumability(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	embedder := newFakeEmbedder()
	embedder.set("search_document: cat smile", unitVec2D(1, 0))
	embedder.set("search_document: bonjour\nsalut\nhey", unitVec2D(0, 1))
	// Simulate a transient embedding error on one trigger during the first run.
	embedder.setErr("search_document: retry me", errors.New("temporary ollama timeout"))

	svc := NewService(repo, embedder, defaultEnabledConfig())

	jsonl1 := strings.Join([]string{
		`{"url":"https://tenor.com/view/cat-smile-1","kind":"gif","description":"cat smile","guild_id":"g1","uses":5,"first_seen":"2024-01-01T10:00:00Z","last_seen":"2024-05-01T12:00:00Z","trigger_texts":["bonjour","salut","hey","extra fourth"]}`,
		`{"url":"https://cdn.discordapp.com/attachments/1/2/a.gif?ex=123","kind":"gif","description":"","guild_id":"g1","uses":2,"first_seen":"2024-02-01T00:00:00Z","last_seen":"2024-03-01T00:00:00Z","trigger_texts":["retry me"]}`,
		`not valid json`,
		`{"url":"","kind":"gif","guild_id":"g1"}`,
	}, "\n")

	sum1, err := svc.ImportJSONL(ctx, strings.NewReader(jsonl1), nil)
	require.NoError(t, err)
	assert.Equal(t, 2, sum1.Rows)
	assert.Equal(t, 2, sum1.EmbeddingsComputed) // cat smile desc + trigger; retry me failed; empty desc skipped
	assert.Equal(t, 0, sum1.EmbeddingsReused)
	assert.Equal(t, 2, sum1.Skipped)

	e1, err := repo.GetEntry(ctx, "https://tenor.com/view/cat-smile-1", "g1")
	require.NoError(t, err)
	require.NotNil(t, e1)
	assert.Equal(t, int64(5), e1.Uses)
	assert.Equal(t, "bonjour\nsalut\nhey", e1.TriggerText)
	assert.Len(t, e1.DescriptionEmbedding, EmbeddingDim*4)
	assert.Len(t, e1.TriggerEmbedding, EmbeddingDim*4)

	e2, err := repo.GetEntry(ctx, "https://cdn.discordapp.com/attachments/1/2/a.gif", "g1")
	require.NoError(t, err)
	require.NotNil(t, e2)
	assert.Empty(t, e2.DescriptionEmbedding)
	assert.Empty(t, e2.TriggerEmbedding) // failed on run 1

	// Second run: fix transient error, update uses/last_seen on row 1 without changing text.
	embedder.resetCalls()
	embedder.setErr("search_document: retry me", nil)
	embedder.set("search_document: retry me", unitVec2D(1, 1))

	jsonl2 := strings.Join([]string{
		`{"url":"https://tenor.com/view/cat-smile-1","kind":"gif","description":"cat smile","guild_id":"g1","uses":9,"first_seen":"2024-01-01T10:00:00Z","last_seen":"2024-06-01T12:00:00Z","trigger_texts":["bonjour","salut","hey"]}`,
		`{"url":"https://cdn.discordapp.com/attachments/1/2/a.gif?ex=999","kind":"gif","description":"","guild_id":"g1","uses":4,"first_seen":"2024-02-01T00:00:00Z","last_seen":"2024-04-01T00:00:00Z","trigger_texts":["retry me"]}`,
	}, "\n")

	sum2, err := svc.ImportJSONL(ctx, strings.NewReader(jsonl2), nil)
	require.NoError(t, err)
	assert.Equal(t, 2, sum2.Rows)
	assert.Equal(t, 1, sum2.EmbeddingsComputed) // only the previously missing "retry me" trigger
	assert.Equal(t, 2, sum2.EmbeddingsReused)   // row 1 description + trigger reused
	assert.Equal(t, 0, sum2.Skipped)
	assert.Equal(t, []string{"search_document: retry me"}, embedder.callHistory())

	e1After, err := repo.GetEntry(ctx, "https://tenor.com/view/cat-smile-1", "g1")
	require.NoError(t, err)
	assert.Equal(t, int64(9), e1After.Uses)
	wantLast, _ := time.Parse(time.RFC3339, "2024-06-01T12:00:00Z")
	assert.Equal(t, wantLast.Unix(), e1After.LastSeen)

	e2After, err := repo.GetEntry(ctx, "https://cdn.discordapp.com/attachments/1/2/a.gif", "g1")
	require.NoError(t, err)
	assert.Equal(t, int64(4), e2After.Uses)
	assert.Len(t, e2After.TriggerEmbedding, EmbeddingDim*4)
}

func TestSearchCLIHelper(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/a-1",
		Kind:                 "gif",
		Description:          "alpha",
		Uses:                 5,
		FirstSeen:            100,
		LastSeen:             100,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.95)),
		TriggerText:          "trig a",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.60)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://www.youtube.com/watch?v=b",
		Kind:                 "youtube",
		Description:          "beta",
		Uses:                 2,
		FirstSeen:            100,
		LastSeen:             100,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.75)),
		TriggerText:          "trig b",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.92)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g2",
		URL:                  "https://tenor.com/view/other-guild-3",
		Kind:                 "gif",
		Description:          "gamma",
		Uses:                 99,
		FirstSeen:            100,
		LastSeen:             100,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.99)),
		TriggerText:          "trig g2",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.99)),
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: query", unitVec2D(1, 0))
	svc := NewService(repo, embedder, defaultEnabledConfig())

	descHits, err := svc.Search(ctx, "g1", "query", false, 10)
	require.NoError(t, err)
	require.Len(t, descHits, 2)
	assert.Equal(t, "https://tenor.com/view/a-1", descHits[0].Entry.URL)
	assert.InDelta(t, 0.95, descHits[0].Similarity, 1e-3)
	assert.Equal(t, "https://www.youtube.com/watch?v=b", descHits[1].Entry.URL)
	assert.InDelta(t, 0.75, descHits[1].Similarity, 1e-3)

	trigHits, err := svc.Search(ctx, "g1", "query", true, 10)
	require.NoError(t, err)
	require.Len(t, trigHits, 2)
	assert.Equal(t, "https://www.youtube.com/watch?v=b", trigHits[0].Entry.URL)
	assert.InDelta(t, 0.92, trigHits[0].Similarity, 1e-3)
	assert.Equal(t, "https://tenor.com/view/a-1", trigHits[1].Entry.URL)
	assert.InDelta(t, 0.60, trigHits[1].Similarity, 1e-3)
}
