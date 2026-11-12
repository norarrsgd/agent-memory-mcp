package sqlite

import (
	"context"
	"database/sql"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func temporaryStore(t *testing.T) *Store {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "private", "db"), 50)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestBusyAndRollback(t *testing.T) {
	s := temporaryStore(t)
	ctx := context.Background()
	conn, err := s.write.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", "file:"+s.Path+"?_pragma=busy_timeout(30)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.ExecContext(ctx, "BEGIN IMMEDIATE"); domain.SafeError(mapError(err)).Code != "busy" {
		t.Fatalf("expected busy: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "INSERT INTO projects VALUES('uncommitted','{}')"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err = s.ProjectExists(ctx, "uncommitted"); domain.SafeError(err).Code != "not_found" {
		t.Fatalf("rollback failed: %v", err)
	}
}
func TestConcurrentInitializationAndReaders(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private", "db")
	if err := PrivatePath(path, true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path, 5000)
			if err == nil {
				_, err = s.Status(context.Background())
				s.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestMigrationFailurePreservesState(t *testing.T) {
	s := temporaryStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "UPDATE schema_migrations SET checksum='bad'"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); domain.SafeError(err).Code != "schema" {
		t.Fatalf("checksum not rejected: %v", err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(99,'future')"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); domain.SafeError(err).Code != "schema" {
		t.Fatalf("newer schema not rejected: %v", err)
	}
}
func TestSearchQueryPlan(t *testing.T) {
	s := temporaryStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := s.read.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT m.id FROM memories_fts JOIN memories m ON m.rowid=memories_fts.rowid WHERE memories_fts MATCH ? AND m.status='active' AND (m.project_key=? OR m.scope='global') ORDER BY bm25(memories_fts),m.updated_at DESC,m.id LIMIT 5`, `"migration"`, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}
