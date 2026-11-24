package utils

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenLocalDBSetsPragmas(t *testing.T) {
	db, err := openLocalDB(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatalf("openLocalDB: %v", err)
	}
	defer db.Close()

	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if timeout != localBusyTimeoutMS {
		t.Fatalf("busy_timeout = %d, want %d", timeout, localBusyTimeoutMS)
	}

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// A writer that finds the database locked by another connection waits for
// the lock instead of failing with "database is locked".
func TestOpenLocalDBWaitsForLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.db")
	holder, err := openLocalDB(path)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	waiter, err := openLocalDB(path)
	if err != nil {
		t.Fatalf("open waiter: %v", err)
	}
	defer waiter.Close()

	if _, err := holder.Exec("CREATE TABLE t (v INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	ctx := context.Background()
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO t (v) VALUES (1)"); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := waiter.Exec("INSERT INTO t (v) VALUES (2)")
		done <- err
	}()

	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waiting insert failed: %v", err)
		}
	case <-time.After(localBusyTimeoutMS * time.Millisecond):
		t.Fatal("waiting insert did not finish")
	}

	var count int
	if err := holder.QueryRow("SELECT count(*) FROM t").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}
