package reengage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	_ "github.com/tursodatabase/go-libsql"
	"go.uber.org/zap"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
)

func TestResolveReengageSettings_UsesConfigDefaults(t *testing.T) {
	ctx := context.Background()
	repo := databaseTestRepository(t)
	if err := repo.SetReengageEnabled(ctx, "guild-defaults", "channel-defaults", true); err != nil {
		t.Fatalf("failed to enable reengage: %v", err)
	}

	service := &Service{
		repo: repo,
		config: config.ReengageConfig{
			DefaultInactivityMinutes: 42,
			DefaultChance:            0.42,
		},
	}

	enabled, threshold, chance := service.resolveReengageSettings(ctx, "guild-defaults", "channel-defaults")

	if !enabled {
		t.Fatalf("expected reengage to remain enabled")
	}
	if threshold != 42 {
		t.Fatalf("expected threshold 42, got %d", threshold)
	}
	if chance != 0.42 {
		t.Fatalf("expected chance 0.42, got %f", chance)
	}
}

func TestResolveReengageSettings_UsesStoredOverrides(t *testing.T) {
	ctx := context.Background()
	repo := databaseTestRepository(t)

	if err := repo.SetReengageEnabled(ctx, "guild-overrides", "channel-overrides", true); err != nil {
		t.Fatalf("failed to set enabled: %v", err)
	}
	if err := repo.SetReengageThreshold(ctx, "guild-overrides", "channel-overrides", 15); err != nil {
		t.Fatalf("failed to set threshold: %v", err)
	}
	if err := repo.SetReengageChance(ctx, "guild-overrides", "channel-overrides", 0.15); err != nil {
		t.Fatalf("failed to set chance: %v", err)
	}

	service := &Service{
		repo: repo,
		config: config.ReengageConfig{
			DefaultInactivityMinutes: 42,
			DefaultChance:            0.42,
		},
	}

	enabled, threshold, chance := service.resolveReengageSettings(ctx, "guild-overrides", "channel-overrides")

	if !enabled {
		t.Fatalf("expected reengage to remain enabled")
	}
	if threshold != 15 {
		t.Fatalf("expected threshold 15, got %d", threshold)
	}
	if chance != 0.15 {
		t.Fatalf("expected chance 0.15, got %f", chance)
	}
}

func TestShouldReengage_SuppressesActiveChannels(t *testing.T) {
	service := &Service{}
	now := time.Unix(2_000_000, 0)
	activeMessage := now.Add(-5 * time.Minute).Unix()

	if service.shouldReengage(activeMessage, 30, 1.0, 0.0, now) {
		t.Fatalf("expected active channel to be suppressed")
	}
}

func TestShouldReengage_AllowsInactiveChannelsWhenChancePasses(t *testing.T) {
	service := &Service{}
	now := time.Unix(2_000_000, 0)
	inactiveMessage := now.Add(-45 * time.Minute).Unix()

	if !service.shouldReengage(inactiveMessage, 30, 0.5, 0.1, now) {
		t.Fatalf("expected inactive channel to reengage when chance roll passes")
	}
}

func TestCheckAllChannels_SkipsGuildsMissingFromGatewayCache(t *testing.T) {
	ctx := context.Background()
	repo := databaseTestRepository(t)
	const leftGuildID = "400000000000000001"
	const channelID = "500000000000000001"

	if err := repo.SetReengageEnabled(ctx, leftGuildID, channelID, true); err != nil {
		t.Fatalf("failed to enable reengage: %v", err)
	}
	if err := repo.SetChannelLastMessage(ctx, leftGuildID, channelID, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("failed to set last message: %v", err)
	}

	transport := &countingTransport{}
	client, err := disgo.New("MTIzNDU2Nzg5MDEyMzQ1Njc4.fake.fake",
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds)),
		bot.WithRestClientConfigOpts(rest.WithHTTPClient(&http.Client{Transport: transport})),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	// The gateway cache holds another guild only, so the reengage guild is one the bot left.
	client.Caches.AddGuild(discord.CacheGuild{Guild: discord.Guild{ID: snowflake.MustParse("600000000000000001")}})

	service := &Service{
		client:        client,
		repo:          repo,
		config:        config.ReengageConfig{DefaultInactivityMinutes: 30, DefaultChance: 1},
		skippedGuilds: make(map[string]struct{}),
	}

	if err := service.CheckAllChannels(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := transport.calls(); calls != 0 {
		t.Fatalf("expected no REST calls for guilds the bot left, got %d", calls)
	}
}

func TestSkipUnknownGuild(t *testing.T) {
	silenceLogs(t)
	service := &Service{skippedGuilds: make(map[string]struct{})}

	if !service.skipUnknownGuild(&rest.Error{Code: rest.JSONErrorCodeUnknownGuild}, "42") {
		t.Fatalf("expected unknown guild error to be handled")
	}
	if !service.isGuildSkipped("42") {
		t.Fatalf("expected guild 42 to be recorded as skipped")
	}

	if service.skipUnknownGuild(fmt.Errorf("wrapped: %w", &rest.Error{Code: rest.JSONErrorCodeMissingAccess}), "43") {
		t.Fatalf("expected unrelated Discord error to be reported as failure")
	}
	if service.skipUnknownGuild(errors.New("connection reset"), "44") {
		t.Fatalf("expected non-Discord error to be reported as failure")
	}
	if service.isGuildSkipped("43") || service.isGuildSkipped("44") {
		t.Fatalf("expected only the unknown guild to be skipped")
	}
}

// countingTransport records any Discord REST call so tests can assert none happened.
type countingTransport struct {
	mu    sync.Mutex
	count int
}

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.count++
	t.mu.Unlock()
	return nil, errors.New("unexpected Discord REST call")
}

func (t *countingTransport) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

func silenceLogs(t *testing.T) {
	t.Helper()

	previous := logger.Log
	logger.Log = zap.NewNop()
	t.Cleanup(func() { logger.Log = previous })
}

func databaseTestRepository(t *testing.T) *database.Repository {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	databaseConn, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	schemaPath := filepath.Join("..", "schema.sql")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		databaseConn.Close()
		t.Fatalf("failed to read schema: %v", err)
	}

	if _, err := databaseConn.Exec(string(schema)); err != nil {
		databaseConn.Close()
		t.Fatalf("failed to create tables: %v", err)
	}

	repo := database.NewRepositoryWithDB(databaseConn)

	t.Cleanup(func() {
		databaseConn.Close()
	})

	return repo
}

// unknownGuildTransport answers every Discord REST call with 404 Unknown Guild.
type unknownGuildTransport struct {
	countingTransport
}

func (t *unknownGuildTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.count++
	t.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"Unknown Guild","code":10004}`)),
		Request:    req,
	}, nil
}

// A guild the bot left while disconnected stays in the gateway cache (no
// GUILD_DELETE). The first run learns it is gone from REST; later runs must not
// call REST for it again.
func TestCheckAllChannels_EvictsStaleCachedGuildOnUnknownGuild(t *testing.T) {
	silenceLogs(t)
	ctx := context.Background()
	repo := databaseTestRepository(t)
	const staleGuildID = "400000000000000002"
	const channelID = "500000000000000002"

	if err := repo.SetReengageEnabled(ctx, staleGuildID, channelID, true); err != nil {
		t.Fatalf("failed to enable reengage: %v", err)
	}
	if err := repo.SetChannelLastMessage(ctx, staleGuildID, channelID, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("failed to set last message: %v", err)
	}

	transport := &unknownGuildTransport{}
	client, err := disgo.New("MTIzNDU2Nzg5MDEyMzQ1Njc4.fake.fake",
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds)),
		bot.WithRestClientConfigOpts(rest.WithHTTPClient(&http.Client{Transport: transport})),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	staleID := snowflake.MustParse(staleGuildID)
	client.Caches.AddGuild(discord.CacheGuild{Guild: discord.Guild{ID: staleID}})
	client.Caches.AddGuild(discord.CacheGuild{Guild: discord.Guild{ID: snowflake.MustParse("600000000000000001")}})

	service := &Service{
		client:        client,
		repo:          repo,
		config:        config.ReengageConfig{DefaultInactivityMinutes: 30, DefaultChance: 1},
		skippedGuilds: make(map[string]struct{}),
	}

	for run := 1; run <= 3; run++ {
		if err := service.CheckAllChannels(ctx); err != nil {
			t.Fatalf("run %d: unexpected error: %v", run, err)
		}
	}
	if calls := transport.calls(); calls != 1 {
		t.Fatalf("expected a single REST call across runs, got %d", calls)
	}
	if _, ok := client.Caches.Guild(staleID); ok {
		t.Fatal("expected the stale guild to be evicted from the gateway cache")
	}
}
