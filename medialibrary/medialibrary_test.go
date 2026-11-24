package medialibrary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/db"
	"polynux/disgoroq/logger"
)

func setupMediaTestDB(t *testing.T) (*sql.DB, *Repository) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	conn, err := sql.Open("libsql", "file:"+dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(time.Hour)

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
	embedder.set("search_document: extra fourth\nhey\nsalut", unitVec2D(0, 1))
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
	assert.Equal(t, "extra fourth\nhey\nsalut", e1.TriggerText)
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
		`{"url":"https://tenor.com/view/cat-smile-1","kind":"gif","description":"cat smile","guild_id":"g1","uses":9,"first_seen":"2024-01-01T10:00:00Z","last_seen":"2024-06-01T12:00:00Z","trigger_texts":["bonjour","salut","hey","extra fourth"]}`,
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

func TestLibSQLLosslessTextRoundTrip(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()

	urlEndingZ := "https://giphy.com/gifs/dragon-ball-abc123Z"
	descEndingZ := "dragon ball Z"
	trigEndingZ := "watching Z"
	dateURL := "https://tenor.com/view/date-clip-2024Z"
	dateDesc := "2024-05-01T12:00:00Z"
	shortDateDesc := "2024-05-01"

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1Z",
		URL:                  urlEndingZ,
		Kind:                 "gif",
		Description:          descEndingZ,
		Uses:                 4,
		FirstSeen:            100,
		LastSeen:             200,
		TriggerText:          trigEndingZ,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.95)),
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.94)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1Z",
		URL:                  dateURL,
		Kind:                 "gif",
		Description:          dateDesc,
		Uses:                 3,
		FirstSeen:            100,
		LastSeen:             200,
		TriggerText:          shortDateDesc,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.90)),
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.89)),
	}))

	// Direct GetEntry round-trip.
	gotZ, err := repo.GetEntry(ctx, urlEndingZ, "g1Z")
	require.NoError(t, err)
	require.NotNil(t, gotZ)
	assert.Equal(t, "g1Z", gotZ.GuildID)
	assert.Equal(t, urlEndingZ, gotZ.URL)
	assert.Equal(t, descEndingZ, gotZ.Description)
	assert.Equal(t, trigEndingZ, gotZ.TriggerText)

	gotDate, err := repo.GetEntry(ctx, dateURL, "g1Z")
	require.NoError(t, err)
	require.NotNil(t, gotDate)
	assert.Equal(t, dateURL, gotDate.URL)
	assert.Equal(t, dateDesc, gotDate.Description)
	assert.Equal(t, shortDateDesc, gotDate.TriggerText)

	// Exact, vector, and Search CLI paths all preserve trailing Z and date strings.
	embedder := newFakeEmbedder()
	embedder.set("search_query: anime", unitVec2D(1, 0))
	embedder.set("search_query: trig", unitVec2D(1, 0))
	svc := NewService(repo, embedder, defaultEnabledConfig())

	exactRes, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1Z",
		RawReply: "[gif: " + descEndingZ + "]",
	})
	require.True(t, ok)
	assert.Equal(t, urlEndingZ, exactRes.URL)

	dateExactRes, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1Z",
		RawReply: "[gif: " + dateDesc + "]",
	})
	require.True(t, ok)
	assert.Equal(t, dateURL, dateExactRes.URL)

	vecRes, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1Z",
		RawReply: "[gif: anime]",
	})
	require.True(t, ok)
	assert.Equal(t, urlEndingZ, vecRes.URL)

	trigRes, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:    "g1Z",
		RawReply:   "[gif]",
		TargetText: "trig",
	})
	require.True(t, ok)
	assert.Equal(t, urlEndingZ, trigRes.URL)

	searchHits, err := svc.Search(ctx, "g1Z", "anime", false, 10)
	require.NoError(t, err)
	require.Len(t, searchHits, 2)
	assert.Equal(t, urlEndingZ, searchHits[0].Entry.URL)
	assert.Equal(t, descEndingZ, searchHits[0].Entry.Description)
	assert.Equal(t, dateURL, searchHits[1].Entry.URL)
	assert.Equal(t, dateDesc, searchHits[1].Entry.Description)

	// Bot repeat-window query also preserves a trailing Z on a bare URL message.
	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	botID := snowflake.ID(9000)
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(901),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   urlEndingZ,
		CreatedAt: time.Unix(1700000000, 0),
	})
	botMsgs, err := repo.GetRecentBotMessages(ctx, chID.String(), botID.String(), 5)
	require.NoError(t, err)
	require.Equal(t, []string{urlEndingZ}, botMsgs)
}

func TestResolveSQLPreLimitFiltering(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()

	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	botID := snowflake.ID(9000)
	baseTime := time.Unix(1700000000, 0)

	// Insert more Discord CDN rows and more repeat-window rows than lookupCandidateLimit (5),
	// all ranked higher than the single eligible Tenor row.
	for i := 0; i < 8; i++ {
		cdnHost := "https://cdn.discordapp.com"
		if i%2 == 1 {
			cdnHost = "https://media.discordapp.net"
		}
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID:              gID.String(),
			URL:                  fmt.Sprintf("%s/attachments/1/2/cat-%d.gif", cdnHost, i),
			Kind:                 "gif",
			Description:          "cat",
			Uses:                 int64(100 - i),
			FirstSeen:            100,
			LastSeen:             500,
			TriggerText:          "meow",
			DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.99)),
			TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.99)),
		}))
	}

	for i := 0; i < 8; i++ {
		recentURL := fmt.Sprintf("https://tenor.com/view/recent-cat-%d", i)
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID:              gID.String(),
			URL:                  recentURL,
			Kind:                 "gif",
			Description:          "cat",
			Uses:                 int64(80 - i),
			FirstSeen:            100,
			LastSeen:             400,
			TriggerText:          "meow",
			DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.96)),
			TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.96)),
		}))
		insertCachedMessage(t, conn, discord.Message{
			ID:        snowflake.ID(200 + i),
			GuildID:   &gID,
			ChannelID: chID,
			Author:    discord.User{ID: botID, Username: "bot", Bot: true},
			Content:   recentURL,
			CreatedAt: baseTime.Add(time.Duration(i) * time.Minute),
		})
	}

	eligibleURL := "https://tenor.com/view/eligible-cat-99"
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              gID.String(),
		URL:                  eligibleURL,
		Kind:                 "gif",
		Description:          "cat",
		Uses:                 1,
		FirstSeen:            100,
		LastSeen:             100,
		TriggerText:          "meow",
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.85)),
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.85)),
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: kitty", unitVec2D(1, 0))
	embedder.set("search_query: meow now", unitVec2D(1, 0))

	cfg := defaultEnabledConfig()
	cfg.RepeatWindow = 20
	svc := NewService(repo, embedder, cfg)

	// Exact match must still reach the eligible row past 16 ineligible higher-ranked rows.
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: chID.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, eligibleURL, res.URL)

	// Description vector search must still reach the eligible row past 16 ineligible rows.
	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: chID.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: kitty]",
	})
	require.True(t, ok)
	assert.Equal(t, eligibleURL, res.URL)

	// Trigger vector search must still reach the eligible row past 16 ineligible rows.
	res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:    gID.String(),
		ChannelID:  chID.String(),
		BotID:      botID.String(),
		RawReply:   "[gif]",
		TargetText: "meow now",
	})
	require.True(t, ok)
	assert.Equal(t, eligibleURL, res.URL)
}

type blockingEmbedder struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingEmbedder) GenerateEmbedding(ctx context.Context, _ string) ([]float32, error) {
	if b.started != nil {
		select {
		case b.started <- struct{}{}:
		default:
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return unitVec2D(1, 0), nil
	}
}

func TestResolveIndependentShortTimeout(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  "https://tenor.com/view/cat-1",
		Kind:                 "gif",
		Description:          "happy cat",
		Uses:                 3,
		FirstSeen:            100,
		LastSeen:             100,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.92)),
	}))

	// 1. Slow embedder is cut short by LookupTimeoutMs without cancelling caller ctx.
	slow := &blockingEmbedder{release: make(chan struct{})}
	cfg := defaultEnabledConfig()
	cfg.LookupTimeoutMs = 40
	svc := NewService(repo, slow, cfg)

	parentCtx, parentCancel := context.WithTimeout(ctx, 5*time.Second)
	defer parentCancel()

	start := time.Now()
	_, ok := svc.Resolve(parentCtx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: smiling kitty]",
	})
	elapsed := time.Since(start)
	assert.False(t, ok)
	assert.Less(t, elapsed, 500*time.Millisecond)
	assert.NoError(t, parentCtx.Err(), "parent completion context must stay uncancelled")
	close(slow.release)

	// 2. Independent of caller cancellation: a fast lookup succeeds even if parent ctx is already cancelled.
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	fastEmbedder := newFakeEmbedder()
	fastEmbedder.set("search_query: smiling kitty", unitVec2D(1, 0))
	fastSvc := NewService(repo, fastEmbedder, cfg)

	res, ok := fastSvc.Resolve(cancelledCtx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: smiling kitty]",
	})
	require.True(t, ok)
	assert.Equal(t, "https://tenor.com/view/cat-1", res.URL)
}

func TestLiveWorkerBoundedQueueDrainAndErrorSafety(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()
	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	aliceID := snowflake.ID(3001)
	t0 := time.Unix(1700001000, 0)

	// 1. Bounded worker drops when full and drains queued jobs on Close.
	blocker := &blockingEmbedder{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	svc := NewService(repo, blocker, defaultEnabledConfig())
	svc.queueCap = 1

	msg1 := discord.Message{
		ID:        snowflake.ID(1001),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "https://tenor.com/view/job-one-1",
		CreatedAt: t0,
	}
	msg2 := discord.Message{
		ID:        snowflake.ID(1002),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "https://tenor.com/view/job-two-2",
		CreatedAt: t0.Add(time.Second),
	}
	msg3 := discord.Message{
		ID:        snowflake.ID(1003),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "https://tenor.com/view/job-three-3",
		CreatedAt: t0.Add(2 * time.Second),
	}

	require.True(t, svc.EnqueueMessage(msg1))
	<-blocker.started // worker popped msg1 and is blocked in embedder
	require.True(t, svc.EnqueueMessage(msg2), "second job should occupy buffer slot")
	assert.False(t, svc.EnqueueMessage(msg3), "third job should be dropped when bounded queue is full")

	close(blocker.release)
	svc.Close() // drains msg1 and msg2 cleanly before returning
	assert.False(t, svc.EnqueueMessage(msg3), "enqueue after Close must return false")

	e1, err := repo.GetEntry(ctx, "https://tenor.com/view/job-one-1", gID.String())
	require.NoError(t, err)
	require.NotNil(t, e1)
	e2, err := repo.GetEntry(ctx, "https://tenor.com/view/job-two-2", gID.String())
	require.NoError(t, err)
	require.NotNil(t, e2)
	e3, err := repo.GetEntry(ctx, "https://tenor.com/view/job-three-3", gID.String())
	require.NoError(t, err)
	assert.Nil(t, e3, "dropped job should not have been indexed")

	// 2. Failed embedding must NOT advance description or trigger_text past an existing valid embedding.
	embedder := newFakeEmbedder()
	origDescVec := unitVec2D(1, 0)
	origTrigVec := unitVec2D(0, 1)
	embedder.set("search_document: old title", origDescVec)
	embedder.set("search_document: old trigger", origTrigVec)

	liveSvc := NewService(repo, embedder, defaultEnabledConfig())
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              gID.String(),
		URL:                  "https://www.youtube.com/watch?v=keepme",
		Kind:                 "youtube",
		Description:          "old title",
		Uses:                 2,
		FirstSeen:            t0.Unix(),
		LastSeen:             t0.Unix(),
		TriggerText:          "old trigger",
		DescriptionEmbedding: mustBlob(t, origDescVec),
		TriggerEmbedding:     mustBlob(t, origTrigVec),
	}))

	// Fail both new description and new merged trigger embeddings.
	embedder.setErr("search_document: new title", errors.New("ollama down"))
	embedder.setErr("search_document: new trigger\nold trigger", errors.New("ollama down"))

	refID := snowflake.ID(1050)
	insertCachedMessage(t, conn, discord.Message{
		ID:        refID,
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "new trigger",
		CreatedAt: t0.Add(time.Minute),
	})
	require.NoError(t, liveSvc.RecordDiscordMessage(ctx, discord.Message{
		ID:               snowflake.ID(1051),
		GuildID:          &gID,
		ChannelID:        chID,
		Author:           discord.User{ID: aliceID, Username: "alice"},
		Content:          "https://www.youtube.com/watch?v=keepme",
		Embeds:           []discord.Embed{{URL: "https://www.youtube.com/watch?v=keepme", Title: "new title"}},
		MessageReference: &discord.MessageReference{MessageID: &refID},
		CreatedAt:        t0.Add(2 * time.Minute),
	}))

	kept, err := repo.GetEntry(ctx, "https://www.youtube.com/watch?v=keepme", gID.String())
	require.NoError(t, err)
	require.NotNil(t, kept)
	assert.Equal(t, int64(3), kept.Uses)
	assert.Equal(t, "old title", kept.Description, "description must not advance past failed embedding")
	assert.Equal(t, "old trigger", kept.TriggerText, "trigger_text must not advance past failed embedding")
	assert.Equal(t, mustBlob(t, origDescVec), kept.DescriptionEmbedding)
	assert.Equal(t, mustBlob(t, origTrigVec), kept.TriggerEmbedding)

	// 3. GetEntry DB error skips the URL instead of treating it as new.
	closedDB, closedRepo := setupMediaTestDB(t)
	require.NoError(t, closedDB.Close())
	brokenSvc := NewService(closedRepo, newFakeEmbedder(), defaultEnabledConfig())
	require.NoError(t, brokenSvc.RecordDiscordMessage(ctx, discord.Message{
		ID:        snowflake.ID(1099),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "https://tenor.com/view/should-skip-1",
		CreatedAt: t0,
	}))
}

func TestRecordDiscordMessageUpdateUnfurlAndDedupe(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()
	svc := NewService(repo, newFakeEmbedder(), defaultEnabledConfig())

	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	aliceID := snowflake.ID(3001)
	msgID := snowflake.ID(2001)
	t0 := time.Unix(1700002000, 0)

	// Step 1: MESSAGE_CREATE has a Tenor GIF and a YouTube link without embeds yet.
	createMsg := discord.Message{
		ID:        msgID,
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   "https://tenor.com/view/cat-jump-1 https://www.youtube.com/watch?v=unfurl1",
		CreatedAt: t0,
	}
	require.NoError(t, svc.RecordDiscordMessage(ctx, createMsg))

	gifEntry, err := repo.GetEntry(ctx, "https://tenor.com/view/cat-jump-1", gID.String())
	require.NoError(t, err)
	require.NotNil(t, gifEntry)
	assert.Equal(t, int64(1), gifEntry.Uses)

	ytBefore, err := repo.GetEntry(ctx, "https://www.youtube.com/watch?v=unfurl1", gID.String())
	require.NoError(t, err)
	assert.Nil(t, ytBefore, "undescribed YouTube link should not be indexed before unfurl")

	// Step 2: MESSAGE_UPDATE arrives with the unfurled YouTube embed.
	updateMsg := createMsg
	updateMsg.Embeds = []discord.Embed{
		{
			URL:   "https://www.youtube.com/watch?v=unfurl1",
			Title: "Unfurled Video Title",
		},
	}
	require.NoError(t, svc.RecordDiscordMessageUpdate(ctx, updateMsg))

	ytAfter, err := repo.GetEntry(ctx, "https://www.youtube.com/watch?v=unfurl1", gID.String())
	require.NoError(t, err)
	require.NotNil(t, ytAfter)
	assert.Equal(t, "youtube", ytAfter.Kind)
	assert.Equal(t, "Unfurled Video Title", ytAfter.Description)
	assert.Equal(t, int64(1), ytAfter.Uses)

	gifAfter, err := repo.GetEntry(ctx, "https://tenor.com/view/cat-jump-1", gID.String())
	require.NoError(t, err)
	assert.Equal(t, int64(1), gifAfter.Uses, "GIF uses must not be counted twice on MESSAGE_UPDATE")

	// Step 3: A second MESSAGE_UPDATE for the same message must not double-count YouTube uses.
	require.NoError(t, svc.RecordDiscordMessageUpdate(ctx, updateMsg))
	ytFinal, err := repo.GetEntry(ctx, "https://www.youtube.com/watch?v=unfurl1", gID.String())
	require.NoError(t, err)
	assert.Equal(t, int64(1), ytFinal.Uses, "repeat MESSAGE_UPDATE must not double-count uses")
}

func TestLiveTriggerPlainV1AndDeepScan(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()
	svc := NewService(repo, newFakeEmbedder(), defaultEnabledConfig())

	gID := snowflake.ID(1000)
	chID := snowflake.ID(2000)
	aliceID := snowflake.ID(3001)
	bobID := snowflake.ID(3002)
	botID := snowflake.ID(9000)
	t0 := time.Unix(1700003000, 0)

	// Previous human message with >300 chars and a raw <@id> mention, followed by 25 bot messages
	// all within the 5-minute window (proves we scan past the old 20-message cap and keep plain v1 text).
	longHumanText := "<@3001> " + strings.Repeat("bonjour ", 45) + "fin"
	require.Greater(t, len(longHumanText), 300)
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(3000),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: aliceID, Username: "alice"},
		Content:   longHumanText,
		CreatedAt: t0.Add(-3 * time.Minute),
	})
	for i := 0; i < 25; i++ {
		insertCachedMessage(t, conn, discord.Message{
			ID:        snowflake.ID(3001 + i),
			GuildID:   &gID,
			ChannelID: chID,
			Author:    discord.User{ID: botID, Username: "bot", Bot: true},
			Content:   "bot chatter",
			CreatedAt: t0.Add(-2*time.Minute + time.Duration(i)*time.Second),
		})
	}

	msg := discord.Message{
		ID:        snowflake.ID(3100),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: bobID, Username: "bob"},
		Content:   "https://tenor.com/view/deep-scan-1",
		CreatedAt: t0,
	}
	require.NoError(t, svc.RecordDiscordMessage(ctx, msg))

	entry, err := repo.GetEntry(ctx, "https://tenor.com/view/deep-scan-1", gID.String())
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, longHumanText, entry.TriggerText)
}

func TestImportMissingDatesAndConsecutiveEmbedFailureAbort(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	// 1. Missing dates default to last_seen / now instead of 0.
	embedder := newFakeEmbedder()
	svc := NewService(repo, embedder, defaultEnabledConfig())

	before := time.Now().Unix()
	jsonlDates := strings.Join([]string{
		`{"url":"https://tenor.com/view/no-dates-1","kind":"gif","description":"no dates","guild_id":"g1","uses":1}`,
		`{"url":"https://tenor.com/view/only-last-2","kind":"gif","description":"only last","guild_id":"g1","uses":1,"last_seen":"2024-05-01T12:00:00Z"}`,
		`{"url":"https://tenor.com/view/only-first-3","kind":"gif","description":"only first","guild_id":"g1","uses":1,"first_seen":"2024-04-01T12:00:00Z"}`,
	}, "\n")

	sum, err := svc.ImportJSONL(ctx, strings.NewReader(jsonlDates), nil)
	require.NoError(t, err)
	after := time.Now().Unix()
	assert.Equal(t, 3, sum.Rows)

	e1, err := repo.GetEntry(ctx, "https://tenor.com/view/no-dates-1", "g1")
	require.NoError(t, err)
	require.NotNil(t, e1)
	assert.GreaterOrEqual(t, e1.FirstSeen, before)
	assert.LessOrEqual(t, e1.FirstSeen, after)
	assert.Equal(t, e1.FirstSeen, e1.LastSeen)

	wantLast, _ := time.Parse(time.RFC3339, "2024-05-01T12:00:00Z")
	e2, err := repo.GetEntry(ctx, "https://tenor.com/view/only-last-2", "g1")
	require.NoError(t, err)
	assert.Equal(t, wantLast.Unix(), e2.FirstSeen)
	assert.Equal(t, wantLast.Unix(), e2.LastSeen)

	wantFirst, _ := time.Parse(time.RFC3339, "2024-04-01T12:00:00Z")
	e3, err := repo.GetEntry(ctx, "https://tenor.com/view/only-first-3", "g1")
	require.NoError(t, err)
	assert.Equal(t, wantFirst.Unix(), e3.FirstSeen)
	assert.Equal(t, wantFirst.Unix(), e3.LastSeen)

	// 2. Abort after 20 consecutive embedding failures; a success resets the counter.
	failEmbedder := newFakeEmbedder()
	var lines []string
	// 19 failures, then 1 success (resets counter), then 20 consecutive failures (aborts).
	for i := 0; i < 19; i++ {
		desc := fmt.Sprintf("fail-a-%d", i)
		failEmbedder.setErr("search_document: "+desc, errors.New("embed err"))
		lines = append(lines, fmt.Sprintf(`{"url":"https://tenor.com/view/fa-%d","kind":"gif","description":%q,"guild_id":"g1","uses":1}`, i, desc))
	}
	lines = append(lines, `{"url":"https://tenor.com/view/ok-reset","kind":"gif","description":"ok-reset","guild_id":"g1","uses":1}`)
	for i := 0; i < 25; i++ {
		desc := fmt.Sprintf("fail-b-%d", i)
		failEmbedder.setErr("search_document: "+desc, errors.New("embed err"))
		lines = append(lines, fmt.Sprintf(`{"url":"https://tenor.com/view/fb-%d","kind":"gif","description":%q,"guild_id":"g1","uses":1}`, i, desc))
	}

	abortSvc := NewService(repo, failEmbedder, defaultEnabledConfig())
	abortSum, err := abortSvc.ImportJSONL(ctx, strings.NewReader(strings.Join(lines, "\n")), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20 consecutive embedding failures")
	// 19 failed rows + 1 ok row + 19 failed rows before the 20th aborted = 39 rows written.
	assert.Equal(t, 39, abortSum.Rows)
}

func captureMediaLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zap.InfoLevel)
	prev := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() {
		logger.Log = prev
	})
	return logs
}

func TestIsJunkGifDescription(t *testing.T) {
	junkCases := []string{
		"togif",
		"ToGif",
		"ezgif com apng to converter",
		"ezgif-5-ab12cd34",
		"ezgif com gif maker",
		"mp4 to gif converter",
		"image0",
		"video12",
		"1234",
		"123456",
		"a1b2c3d4",
		"---",
		"x",
		"tenor gif",
		"unknown",
		"attachment",
	}
	for _, tc := range junkCases {
		assert.True(t, isJunkGifDescription(tc), "expected %q to be detected as junk", tc)
	}

	validCases := []string{
		"cat",
		"cat smile",
		"ok",
		"gg",
		"to be continued",
		"dragon ball Z",
		"mdr",
		"ps5",
		"404",
		"ab12cd",
		"squidgame2",
		"naruto2023",
		"dragonball2024",
		"reverse",
		"reversed",
		"uno reverse",
		"rotate",
		"rotated",
		"split",
		"discord",
		"thumb",
		"frame",
		"frames",
		"ezgif funny cat",
		"happy cat 2024",
		"pleure de rire",
	}
	for _, tc := range validCases {
		assert.False(t, isJunkGifDescription(tc), "expected %q NOT to be detected as junk", tc)
	}

	assert.Equal(t, "funny cat", cleanGifDescription("ezgif funny cat"))
	assert.Equal(t, "funny cat", cleanGifDescription("ezgif com funny cat"))
	assert.Equal(t, "to be continued", cleanGifDescription("to be continued"))
	assert.Equal(t, "dragon ball Z", cleanGifDescription("dragon ball Z"))
}

func TestResolveRandomGifFallbackTriggersAndLogs(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	fallbackURL := "https://tenor.com/view/guild-favorite-1"
	exactCatURL := "https://tenor.com/view/exact-cat-2"

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  fallbackURL,
		Kind:                 "gif",
		Description:          "dancing duck",
		Uses:                 5,
		FirstSeen:            100,
		LastSeen:             300,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.20)),
		TriggerText:          "dance",
		TriggerEmbedding:     mustBlob(t, vecWithSimilarityToXAxis(0.20)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:              "g1",
		URL:                  exactCatURL,
		Kind:                 "gif",
		Description:          "cat smile",
		Uses:                 1,
		FirstSeen:            100,
		LastSeen:             200,
		DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.20)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:     "g2",
		URL:         "https://tenor.com/view/other-guild-popular-9",
		Kind:        "gif",
		Description: "other guild",
		Uses:        50,
		FirstSeen:   100,
		LastSeen:    500,
	}))

	embedder := newFakeEmbedder()
	unrelatedVec := make([]float32, EmbeddingDim)
	unrelatedVec[2] = 1
	embedder.set("search_query: totally unmatched query", unrelatedVec)
	embedder.set("search_query: random human chat", unrelatedVec)
	embedder.setErr("search_query: broken embed", errors.New("ollama 500"))
	embedder.setErr("search_query: broken trigger", errors.New("ollama 500"))

	t.Run("disabled keeps exact existing behavior", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.RandomGifFallback = false
		svc := NewService(repo, embedder, cfg)

		for _, req := range []ai.MediaResolveRequest{
			{GuildID: "g1", RawReply: "[gif: totally unmatched query]"},
			{GuildID: "g1", RawReply: "[gif: togif]"},
			{GuildID: "g1", RawReply: "[gif: ezgif com apng to converter]"},
			{GuildID: "g1", RawReply: "[gif: broken embed]"},
			{GuildID: "g1", RawReply: "[gif]", TargetText: "random human chat"},
			{GuildID: "g1", RawReply: "[gif]", TargetText: "broken trigger"},
		} {
			_, ok := svc.Resolve(ctx, req)
			assert.False(t, ok, "fallback must not trigger when disabled for %q", req.RawReply)
		}
	})

	cfg := defaultEnabledConfig()
	cfg.RandomGifFallback = true
	svc := NewService(repo, embedder, cfg)

	assertFallbackLog := func(t *testing.T, logs *observer.ObservedLogs, wantTag, wantReason, wantURL string) {
		t.Helper()
		entries := logs.FilterMessage("Media library random gif fallback").All()
		require.Len(t, entries, 1)
		ctxMap := entries[0].ContextMap()
		assert.Equal(t, wantTag, ctxMap["tag"])
		assert.Equal(t, wantReason, ctxMap["reason"])
		assert.Equal(t, wantURL, ctxMap["url"])
	}

	t.Run("triggers on no_match for described and bare gif", func(t *testing.T) {
		logs := captureMediaLogs(t)
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "mdr [gif: totally unmatched query]",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		assertFallbackLog(t, logs, "[gif: totally unmatched query]", "no_match", fallbackURL)

		logs2 := captureMediaLogs(t)
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:    "g1",
			RawReply:   "[gif]",
			TargetText: "random human chat",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		assertFallbackLog(t, logs2, "[gif]", "no_match", fallbackURL)
	})

	t.Run("triggers on junk without calling embedder", func(t *testing.T) {
		for _, rawTag := range []string{"[gif: togif]", "[gif: ezgif com apng to converter]"} {
			embedder.resetCalls()
			logs := captureMediaLogs(t)
			res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
				GuildID:  "g1",
				RawReply: "tiens " + rawTag,
			})
			require.True(t, ok)
			assert.Equal(t, fallbackURL, res.URL)
			assert.Empty(t, embedder.callHistory(), "junk tag %s should skip embedding", rawTag)
			assertFallbackLog(t, logs, rawTag, "junk", fallbackURL)
		}
	})

	t.Run("triggers on embedder error for described and bare gif", func(t *testing.T) {
		logs := captureMediaLogs(t)
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: broken embed]",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		assertFallbackLog(t, logs, "[gif: broken embed]", "error", fallbackURL)

		logs2 := captureMediaLogs(t)
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:    "g1",
			RawReply:   "[gif]",
			TargetText: "broken trigger",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		assertFallbackLog(t, logs2, "[gif]", "error", fallbackURL)
	})

	t.Run("triggers on embedder timeout within lookup timeout", func(t *testing.T) {
		slow := &blockingEmbedder{release: make(chan struct{})}
		defer close(slow.release)

		timeoutCfg := defaultEnabledConfig()
		timeoutCfg.RandomGifFallback = true
		timeoutCfg.LookupTimeoutMs = 80
		timeoutSvc := NewService(repo, slow, timeoutCfg)

		logs := captureMediaLogs(t)
		parentCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		start := time.Now()
		res, ok := timeoutSvc.Resolve(parentCtx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: slow embed query]",
		})
		elapsed := time.Since(start)

		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		assert.Less(t, elapsed, 500*time.Millisecond)
		assert.NoError(t, parentCtx.Err(), "parent completion context must stay uncancelled")
		assertFallbackLog(t, logs, "[gif: slow embed query]", "error", fallbackURL)
	})

	t.Run("slow earlier non-gif tag does not starve later gif fallback", func(t *testing.T) {
		slow := &blockingEmbedder{release: make(chan struct{})}
		defer close(slow.release)

		timeoutCfg := defaultEnabledConfig()
		timeoutCfg.RandomGifFallback = true
		timeoutCfg.LookupTimeoutMs = 80
		timeoutSvc := NewService(repo, slow, timeoutCfg)

		for _, tc := range []struct {
			name       string
			rawReply   string
			wantTag    string
			wantReason string
		}{
			{
				name:       "youtube followed by junk gif",
				rawReply:   "[youtube: slow video] [gif: togif]",
				wantTag:    "[gif: togif]",
				wantReason: "junk",
			},
			{
				name:       "youtube followed by described gif",
				rawReply:   "[youtube: slow video] [gif: slow gif]",
				wantTag:    "[gif: slow gif]",
				wantReason: "error",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				logs := captureMediaLogs(t)
				parentCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				start := time.Now()
				res, ok := timeoutSvc.Resolve(parentCtx, ai.MediaResolveRequest{
					GuildID:  "g1",
					RawReply: tc.rawReply,
				})
				elapsed := time.Since(start)

				require.True(t, ok)
				assert.Equal(t, fallbackURL, res.URL)
				assert.Less(t, elapsed, 500*time.Millisecond)
				assert.NoError(t, parentCtx.Err(), "parent completion context must stay uncancelled")
				assertFallbackLog(t, logs, tc.wantTag, tc.wantReason, fallbackURL)
			})
		}
	})

	t.Run("strips junk tokens and searches remaining meaningful words", func(t *testing.T) {
		// Exact match after stripping "ezgif": "[gif: ezgif cat smile]" -> searches "cat smile".
		embedder.resetCalls()
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "tiens [gif: ezgif cat smile]",
		})
		require.True(t, ok)
		assert.Equal(t, exactCatURL, res.URL)
		assert.Empty(t, embedder.callHistory())

		// Semantic search uses cleaned query "funny cat", not "ezgif funny cat".
		embedder.resetCalls()
		embedder.set("search_query: funny cat", unrelatedVec)
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "tiens [gif: ezgif funny cat]",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		assert.Equal(t, []string{"search_query: funny cat"}, embedder.callHistory())
	})

	t.Run("link tags stay stripped when unmatched", func(t *testing.T) {
		_, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "regarde [youtube: totally unmatched query]",
		})
		assert.False(t, ok)

		_, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[tweet: broken embed]",
		})
		assert.False(t, ok)
	})

	t.Run("normal match on later tag wins over earlier junk tag", func(t *testing.T) {
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: togif] [gif: cat smile]",
		})
		require.True(t, ok)
		assert.Equal(t, exactCatURL, res.URL)
	})

	t.Run("same guild only even when cross_guild is enabled", func(t *testing.T) {
		crossCfg := defaultEnabledConfig()
		crossCfg.RandomGifFallback = true
		crossCfg.CrossGuild = true
		crossSvc := NewService(repo, embedder, crossCfg)

		// Guild "g3" has no gifs of its own; g2's gifs must NOT be chosen by random fallback.
		_, ok := crossSvc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g3",
			RawReply: "[gif: togif]",
		})
		assert.False(t, ok)
	})
}

func TestResolveRandomGifFallbackPopularityPreference(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()

	gID := snowflake.ID(4100)
	chID := snowflake.ID(4200)
	botID := snowflake.ID(4900)
	baseTime := time.Unix(1700010000, 0)

	popular := map[string]bool{
		"https://tenor.com/view/pop-1": true,
		"https://tenor.com/view/pop-2": true,
	}
	unpopular := map[string]bool{}

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: gID.String(), URL: "https://tenor.com/view/pop-1", Kind: "gif", Uses: 2, FirstSeen: 100, LastSeen: 200,
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: gID.String(), URL: "https://tenor.com/view/pop-2", Kind: "gif", Uses: 10, FirstSeen: 100, LastSeen: 200,
	}))
	for i := 0; i < 8; i++ {
		u := fmt.Sprintf("https://tenor.com/view/single-use-%d", i)
		unpopular[u] = true
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: gID.String(), URL: u, Kind: "gif", Uses: 1, FirstSeen: 100, LastSeen: 200,
		}))
	}

	cfg := defaultEnabledConfig()
	cfg.RandomGifFallback = true
	cfg.RepeatWindow = 10
	svc := NewService(repo, newFakeEmbedder(), cfg)

	// With popular gifs (uses >= 2) eligible, fallback must always pick from popular ones.
	for i := 0; i < 30; i++ {
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  gID.String(),
			RawReply: "[gif: togif]",
		})
		require.True(t, ok)
		assert.True(t, popular[res.URL], "expected popular gif (uses >= 2), got %s", res.URL)
	}

	// Put both popular gifs in the channel's repeat window: fallback must now pick from uses == 1.
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(5001),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   "https://tenor.com/view/pop-1\nhttps://tenor.com/view/pop-2",
		CreatedAt: baseTime,
	})

	for i := 0; i < 20; i++ {
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:   gID.String(),
			ChannelID: chID.String(),
			BotID:     botID.String(),
			RawReply:  "[gif: togif]",
		})
		require.True(t, ok)
		assert.True(t, unpopular[res.URL], "expected fallback to uses == 1 pool when popular gifs are excluded, got %s", res.URL)
	}
}

func TestResolveRandomGifFallbackCDNAndRepeatWindowExclusions(t *testing.T) {
	conn, repo := setupMediaTestDB(t)
	ctx := context.Background()

	gID := snowflake.ID(6100)
	chID := snowflake.ID(6200)
	botID := snowflake.ID(6900)
	baseTime := time.Unix(1700020000, 0)

	// Insert more Discord CDN rows and more repeat-window rows than lookupCandidateLimit (5),
	// all with uses >= 2, plus one non-gif youtube link and one eligible Tenor gif (uses = 1).
	for i := 0; i < 8; i++ {
		host := "https://cdn.discordapp.com"
		if i%2 == 1 {
			host = "https://media.discordapp.net"
		}
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: gID.String(), URL: fmt.Sprintf("%s/attachments/1/2/cdn-%d.gif", host, i),
			Kind: "gif", Uses: 50, FirstSeen: 100, LastSeen: 500,
		}))
	}
	for i := 0; i < 8; i++ {
		recentURL := fmt.Sprintf("https://tenor.com/view/recent-%d", i)
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: gID.String(), URL: recentURL,
			Kind: "gif", Uses: 40, FirstSeen: 100, LastSeen: 400,
		}))
		insertCachedMessage(t, conn, discord.Message{
			ID:        snowflake.ID(6300 + i),
			GuildID:   &gID,
			ChannelID: chID,
			Author:    discord.User{ID: botID, Username: "bot", Bot: true},
			Content:   recentURL,
			CreatedAt: baseTime.Add(time.Duration(i) * time.Minute),
		})
	}
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: gID.String(), URL: "https://www.youtube.com/watch?v=notgif",
		Kind: "youtube", Uses: 99, FirstSeen: 100, LastSeen: 900,
	}))
	eligibleURL := "https://tenor.com/view/only-eligible-1"
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: gID.String(), URL: eligibleURL,
		Kind: "gif", Uses: 1, FirstSeen: 100, LastSeen: 100,
	}))

	cfg := defaultEnabledConfig()
	cfg.RandomGifFallback = true
	cfg.AllowDiscordCDN = false
	cfg.RepeatWindow = 20
	svc := NewService(repo, newFakeEmbedder(), cfg)

	// SQL pre-LIMIT filtering must skip all 16 ineligible rows and return the sole eligible gif.
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: chID.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: togif]",
	})
	require.True(t, ok)
	assert.Equal(t, eligibleURL, res.URL)

	// Also exclude the sole eligible gif via repeat window -> fallback returns false.
	insertCachedMessage(t, conn, discord.Message{
		ID:        snowflake.ID(6400),
		GuildID:   &gID,
		ChannelID: chID,
		Author:    discord.User{ID: botID, Username: "bot", Bot: true},
		Content:   eligibleURL,
		CreatedAt: baseTime.Add(30 * time.Minute),
	})
	_, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: chID.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: togif]",
	})
	assert.False(t, ok)

	// Enabling AllowDiscordCDN makes the popular CDN gifs eligible.
	cfgAllowCDN := cfg
	cfgAllowCDN.AllowDiscordCDN = true
	allowSvc := NewService(repo, newFakeEmbedder(), cfgAllowCDN)
	res, ok = allowSvc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:   gID.String(),
		ChannelID: chID.String(),
		BotID:     botID.String(),
		RawReply:  "[gif: togif]",
	})
	require.True(t, ok)
	assert.True(t, ai.IsDiscordCDNURL(res.URL), "expected CDN gif when AllowDiscordCDN is true, got %s", res.URL)
}

func TestIsGifRequest(t *testing.T) {
	positive := []string{
		"gif",
		"GIF",
		"gifs",
		"GIFs",
		"envoie un gif stp",
		"un gif ?",
		"un gif?",
		"un gif\u00a0?",
		"balance des gifs !",
		"tu peux m'envoyer un GIF de chat.",
		"(gif)",
		"envoie un gif comme [gif: cat]",
		"envoie un gif comme https://tenor.com/view/cat-1",
		"<@12345> un gif stp",
	}
	for _, tc := range positive {
		assert.True(t, isGifRequest(tc), "expected %q to be detected as a gif request", tc)
	}

	negative := []string{
		"",
		"bonjour ça va ?",
		"je vais te mettre une gifle",
		"gifler quelqu'un",
		"he is gifted",
		"antigif",
		"gifé",
		"my_gif",
		"gif2",
		"[gif]",
		"[gif: cat smile]",
		"regarde [gif: funny cat]",
		"https://tenor.com/view/cat-gif-12345",
		"regarde https://example.com/funny.gif",
		"<:gif:123456789>",
		"<a:gif:123456789>",
	}
	for _, tc := range negative {
		assert.False(t, isGifRequest(tc), "expected %q NOT to be detected as a gif request", tc)
	}
}

func TestResolveGifOnRequest(t *testing.T) {
	_, repo := setupMediaTestDB(t)
	ctx := context.Background()

	triggerURL := "https://tenor.com/view/trigger-cat-1"
	fallbackURL := "https://tenor.com/view/random-popular-2"
	exactYoutubeURL := "https://www.youtube.com/watch?v=existing123"

	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:          "g1",
		URL:              triggerURL,
		Kind:             "gif",
		Description:      "happy cat",
		Uses:             1,
		FirstSeen:        100,
		LastSeen:         200,
		TriggerText:      "envoie un gif de chat",
		TriggerEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.92)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:          "g1",
		URL:              fallbackURL,
		Kind:             "gif",
		Description:      "popular dance",
		Uses:             5,
		FirstSeen:        100,
		LastSeen:         300,
		TriggerText:      "autre chose",
		TriggerEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.20)),
	}))
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID:     "g1",
		URL:         exactYoutubeURL,
		Kind:        "youtube",
		Description: "cool song",
		Uses:        3,
		FirstSeen:   100,
		LastSeen:    200,
	}))

	embedder := newFakeEmbedder()
	embedder.set("search_query: envoie un gif de chat stp", unitVec2D(1, 0))
	unrelatedVec := make([]float32, EmbeddingDim)
	unrelatedVec[2] = 1
	embedder.set("search_query: un gif ?", unrelatedVec)
	embedder.setErr("search_query: envoie un gif casse", errors.New("ollama 500"))

	t.Run("disabled does nothing for text-only reply", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = false
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		_, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens frérot",
			TargetText:      "envoie un gif de chat stp",
			TargetMessageID: "m-1",
		})
		assert.False(t, ok)
	})

	t.Run("uses trigger search first when enabled", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens bg",
			TargetText:      "envoie un gif de chat stp",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, triggerURL, res.URL)
	})

	t.Run("falls back to random gif when trigger misses and random_gif_fallback is enabled", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		logs := captureMediaLogs(t)
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "voilà pour toi",
			TargetText:      "un gif ?",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)

		entries := logs.FilterMessage("Media library random gif fallback").All()
		require.Len(t, entries, 1)
		assert.Equal(t, "no_match", entries[0].ContextMap()["reason"])
		assert.Equal(t, fallbackURL, entries[0].ContextMap()["url"])

		// Embedder error also falls back to random gif with reason=error.
		logsErr := captureMediaLogs(t)
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "voilà",
			TargetText:      "envoie un gif casse",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		entriesErr := logsErr.FilterMessage("Media library random gif fallback").All()
		require.Len(t, entriesErr, 1)
		assert.Equal(t, "error", entriesErr[0].ContextMap()["reason"])
	})

	t.Run("does not fall back to random gif when random_gif_fallback is disabled", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = false
		svc := NewService(repo, embedder, cfg)

		// Trigger match still succeeds without random_gif_fallback.
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens",
			TargetText:      "envoie un gif de chat stp",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, triggerURL, res.URL)

		// Trigger miss stays text-only when random_gif_fallback is false.
		_, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens",
			TargetText:      "un gif ?",
			TargetMessageID: "m-1",
		})
		assert.False(t, ok)
	})

	t.Run("does not trigger on negative phrases like gifle or v1 gif tag", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		for _, target := range []string{
			"je vais te mettre une gifle",
			"regarde [gif: cat]",
			"https://tenor.com/view/cat-gif-123",
		} {
			_, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
				GuildID:         "g1",
				RawReply:        "mdr",
				TargetText:      target,
				TargetMessageID: "m-1",
			})
			assert.False(t, ok, "must not trigger gif_on_request for %q", target)
		}
	})

	t.Run("does not append gif when raw reply already contains a URL", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		for _, rawReply := range []string{
			"tiens https://tenor.com/view/foo",
			"https://media.giphy.com/media/123/giphy.gif",
			"tiens www.example.com/funny.gif",
		} {
			embedder.resetCalls()
			_, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
				GuildID:         "g1",
				RawReply:        rawReply,
				TargetText:      "envoie un gif de chat stp",
				TargetMessageID: "m-1",
			})
			assert.False(t, ok, "must not append second URL when raw reply %q already contains a URL", rawReply)
			assert.Empty(t, embedder.callHistory(), "must skip lookup when raw reply %q already contains a URL", rawReply)
		}
	})

	t.Run("does not trigger without TargetMessageID (reengage) while bare gif still uses TargetText", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		// Reengage has no TargetMessageID: stale last human message asking for a gif must NOT trigger gif_on_request.
		embedder.resetCalls()
		_, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:    "g1",
			RawReply:   "personne ici ?",
			TargetText: "envoie un gif de chat stp",
		})
		assert.False(t, ok)
		assert.Empty(t, embedder.callHistory())

		// Bare [gif] tag in reengage still uses TargetText for trigger lookup.
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:    "g1",
			RawReply:   "personne ici ? [gif]",
			TargetText: "envoie un gif de chat stp",
		})
		require.True(t, ok)
		assert.Equal(t, triggerURL, res.URL)
	})

	t.Run("does not override already resolved media URL", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens [youtube: cool song]",
			TargetText:      "envoie un gif de chat stp",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, exactYoutubeURL, res.URL)
	})

	t.Run("resolves gif when unmatched link tag leaves reply without media URL", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens [youtube: unknown video]",
			TargetText:      "un gif ?",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
	})

	t.Run("tries trigger search on answered message before random fallback when both options are on", func(t *testing.T) {
		cfg := defaultEnabledConfig()
		cfg.GifOnRequest = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, embedder, cfg)

		embedder.set("search_query: totally unmatched tag", unrelatedVec)

		// Junk gif tag on a gif request should try trigger search on TargetText before random fallback.
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens [gif: togif]",
			TargetText:      "envoie un gif de chat stp",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, triggerURL, res.URL)

		// Unmatched described gif tag on a gif request should also try trigger search before random fallback.
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens [gif: totally unmatched tag]",
			TargetText:      "envoie un gif de chat stp",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, triggerURL, res.URL)

		// When trigger search misses as well, falls back to random gif and keeps original junk reason.
		logs := captureMediaLogs(t)
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens [gif: togif]",
			TargetText:      "un gif ?",
			TargetMessageID: "m-1",
		})
		require.True(t, ok)
		assert.Equal(t, fallbackURL, res.URL)
		entries := logs.FilterMessage("Media library random gif fallback").All()
		require.Len(t, entries, 1)
		assert.Equal(t, "[gif: togif]", entries[0].ContextMap()["tag"])
		assert.Equal(t, "junk", entries[0].ContextMap()["reason"])
	})
}
