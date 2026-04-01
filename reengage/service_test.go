package reengage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
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
