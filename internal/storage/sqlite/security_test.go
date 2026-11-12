package sqlite

import (
	"context"
	"database/sql"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
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
