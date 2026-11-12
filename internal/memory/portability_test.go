package memory

import (
	"context"
	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
	"os"
	"path/filepath"
	"testing"
)

func TestExportImportAndBackup(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	a, err := svc.Upsert(ctx, input())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := sqlite.Open(filepath.Join(dir, "import", "memory.db"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	target := New(other, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	for i := 0; i < 2; i++ {
		if err = target.Import(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	got, err := target.Get(ctx, domain.GetInput{ID: a.ID})
	if err != nil || got.Memory.Version != 1 {
		t.Fatalf("import %+v %v", got, err)
	}
	snapshot.Memories[0].Content = "conflicting data"
	if err = target.Import(ctx, snapshot); code(err) != "conflict" {
		t.Fatalf("import conflict: %v", err)
	}
	backup := filepath.Join(dir, "backup", "snapshot.db")
	if err = s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err = s.Backup(ctx, backup); err == nil {
		t.Fatal("overwrote backup")
	}
	restored, err := sqlite.Open(backup, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.Get(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Doctor(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe backup permissions")
	}
}

func TestBackupDuringIndependentWrites(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	initial, err := svc.Upsert(ctx, input())
	if err != nil {
		t.Fatal(err)
	}
	other, err := sqlite.Open(s.Path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	writer := New(other, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 30; i++ {
			if _, err := writer.Upsert(ctx, input()); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backups", "backup.db")
	backupErr := s.Backup(ctx, backup)
	writeErr := <-done
	if backupErr != nil {
		t.Fatal(backupErr)
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	restored, err := sqlite.Open(backup, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.Get(ctx, initial.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Doctor(ctx); err != nil {
		t.Fatal(err)
	}
}
