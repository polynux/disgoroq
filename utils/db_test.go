package utils

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenLocalDBSetsPragmas(t *testing.T) {
	db, err := OpenLocalDB(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatalf("OpenLocalDB: %v", err)
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

// A replacement connection gets the pragmas too, not only the first one.
func TestOpenLocalDBPragmasOnNewConnection(t *testing.T) {
	db, err := OpenLocalDB(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatalf("OpenLocalDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	first, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("first conn: %v", err)
	}
	// Drop the pooled connection so the pool must open a new one.
	if err := first.Raw(func(any) error { return driver.ErrBadConn }); err == nil {
		t.Fatal("expected ErrBadConn from Raw")
	}
	first.Close()

	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if timeout != localBusyTimeoutMS {
		t.Fatalf("busy_timeout on new connection = %d, want %d", timeout, localBusyTimeoutMS)
	}
}

// A writer that finds the database locked by another connection waits for
// the lock instead of failing with "database is locked".
func TestOpenLocalDBWaitsForLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.db")
	holder, err := OpenLocalDB(path)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	waiter, err := OpenLocalDB(path)
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
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO t (v) VALUES (1)"); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := waiter.Exec("INSERT INTO t (v) VALUES (2)")
		done <- err
	}()

	<-started
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("insert finished while the lock was held (err: %v); it should wait", err)
	default:
	}
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

type countingConnector struct {
	driver.Connector
	closed int
}

func (c *countingConnector) Close() error {
	c.closed++
	return nil
}

// DB.Close must reach the wrapped connector so libsql frees its native handle.
func TestPragmaConnectorForwardsClose(t *testing.T) {
	inner := &countingConnector{}
	db := sql.OpenDB(pragmaConnector{Connector: inner})
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if inner.closed != 1 {
		t.Fatalf("inner connector closed %d times, want 1", inner.closed)
	}
}
