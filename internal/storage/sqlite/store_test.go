package sqlite

import (
	"context"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"path/filepath"
	"testing"
)

func TestInitializeAndPersistence(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "private", "memory.db")
	s, err := Open(p, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterProject(context.Background(), domain.Project{Key: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(p, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ps, err := s.Projects(context.Background(), "", 5)
	if err != nil || len(ps) != 1 {
		t.Fatalf("%+v %v", ps, err)
	}
}
