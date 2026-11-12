package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivatePaths(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative.db", "file:/tmp/memory.db", filepath.Join(dir, "unsafe", "db")} {
		if path == filepath.Join(dir, "unsafe", "db") {
			if err = os.Mkdir(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err = PrivatePath(path, false); err == nil {
			t.Fatalf("unsafe path accepted %s", path)
		}
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(filepath.Join(dir, "nonexistent"), link); err != nil {
		t.Fatal(err)
	}
	if err = PrivatePath(link, true); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestMigrationRollback(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE projects (sentinel TEXT)"); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path, 100); err == nil {
		s.Close()
		t.Fatal("migration should fail on incompatible schema")
	} else if domain.SafeError(err).Code != "schema" {
		t.Fatalf("expected migration failure, got %v", err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='schema_migrations' OR name='memories'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial migration survived: %d %v", n, err)
	}
}
func TestInterruptedWriterHelper(t *testing.T) {
	if os.Getenv("AGENT_MEMORY_CRASH_HELPER") != "1" {
		return
	}
	db, err := sql.Open("sqlite", os.Getenv("AGENT_MEMORY_CRASH_DB"))
	if err != nil {
		os.Exit(2)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("BEGIN IMMEDIATE"); err != nil {
		os.Exit(3)
	}
	if _, err = db.Exec(`INSERT INTO projects VALUES('crash','{}')`); err != nil {
		os.Exit(4)
	}
	os.Stdout.Write([]byte("ready\n"))
	select {}
}
func TestInterruptedWriteRecovery(t *testing.T) {
	s := temporaryStore(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestInterruptedWriterHelper$")
	cmd.Env = append(os.Environ(), "AGENT_MEMORY_CRASH_HELPER=1", "AGENT_MEMORY_CRASH_DB="+s.Path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	b := make([]byte, 6)
	if _, err = stdout.Read(b); err != nil || string(b) != "ready\n" {
		t.Fatalf("helper not ready %q %v", b, err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if err = s.ProjectExists(context.Background(), "crash"); err == nil {
		t.Fatal("uncommitted write survived crash")
	}
	if _, err = s.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptDatabaseIsPreserved(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "broken.db")
	original := []byte("not a sqlite database; preserve evidence")
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path, 100); err == nil {
		s.Close()
		t.Fatal("corruption accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatal("corrupt database modified")
	}
}

func TestDoctorDetectsFTSMismatch(t *testing.T) {
	s := temporaryStore(t)
	ctx := context.Background()
	m := domain.Memory{ID: "00000000-0000-4000-8000-000000000001", Scope: "global", Type: "fact", Title: "Index test", Content: "searchable content", Tags: []string{}, SourceAgent: "test", Status: "active", Evidence: "observation"}
	if _, err := s.Put(ctx, m, 0, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO memories_fts(memories_fts) VALUES('delete-all')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Doctor(ctx); err == nil {
		t.Fatal("doctor accepted inconsistent index")
	}
}

// A hostile schema (trigger or view) must not run under a trusted schema. The probed unsafe functions are
// absent or disabled in this build, so a behavioral test cannot tell the setting on from off; assert it on both pools.
func TestSchemaIsUntrusted(t *testing.T) {
	s := temporaryStore(t)
	for name, db := range map[string]*sql.DB{"read": s.read, "write": s.write} {
		var trusted int
		if err := db.QueryRow("PRAGMA trusted_schema").Scan(&trusted); err != nil || trusted != 0 {
			t.Fatalf("%s pool trusts the schema: %d %v", name, trusted, err)
		}
	}
}

func TestDatabaseSizeLimit(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenLimited(filepath.Join(dir, "private", "db"), 50, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	body := strings.Repeat("capacity ", 6000)
	var stored []string
	var full error
	for i := 0; i < 2000 && full == nil; i++ {
		m := domain.Memory{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Scope: "global", Type: "fact", Title: "Bulk", Content: body, Tags: []string{}, SourceAgent: "test", Status: "active", Evidence: "observation"}
		if _, full = s.Put(ctx, m, 0, nil, "", 0); full == nil {
			stored = append(stored, m.ID)
		}
	}
	if domain.SafeError(full).Code != "storage_full" {
		t.Fatalf("expected storage_full after %d records, got %v", len(stored), full)
	}
	if len(stored) == 0 {
		t.Fatal("limit rejected the first write")
	}
	if _, err = s.Get(ctx, stored[len(stored)-1]); err != nil {
		t.Fatalf("record stored before the limit is unreadable: %v", err)
	}
	if _, err = s.Doctor(ctx); err != nil {
		t.Fatalf("database damaged by hitting the limit: %v", err)
	}
	if _, err = s.Export(ctx); err != nil {
		t.Fatalf("export unavailable at the limit: %v", err)
	}
	if err = s.Backup(ctx, filepath.Join(dir, "backups", "full.db")); err != nil {
		t.Fatalf("backup unavailable at the limit: %v", err)
	}
	if _, err = OpenLimited(filepath.Join(dir, "other", "db"), 50, 8); err == nil {
		t.Fatal("limit below the minimum accepted")
	}
}

func TestDoctorWarnsAboutWritableAncestors(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(dir, "open")
	if err = os.Mkdir(open, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(open, 0777); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(open, "private", "db"), 50)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	report, err := s.Doctor(context.Background())
	if err != nil {
		t.Fatalf("a warning must not fail the check: %v", err)
	}
	if got, _ := report["ancestor_permissions"].(string); !strings.HasPrefix(got, "warn:") || !strings.Contains(got, open) {
		t.Fatalf("writable ancestor not reported: %v", report["ancestor_permissions"])
	}
}

func TestSizeLimitUsesTheDatabasePageSize(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "small-pages.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"PRAGMA page_size=1024", "CREATE TABLE unrelated(x)"} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := OpenLimited(path, 50, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var pages int
	if err = s.write.QueryRow("PRAGMA max_page_count").Scan(&pages); err != nil || pages != 16*1024*1024/1024 {
		t.Fatalf("1 KiB pages must allow 16384 pages, got %d %v", pages, err)
	}
}
