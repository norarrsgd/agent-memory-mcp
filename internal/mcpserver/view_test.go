package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

func viewSession(t *testing.T, enabled bool) (*mcp.ClientSession, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "private", "memory.db")
	store, err := sqlite.Open(db, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	if err = svc.RegisterProject(ctx, domain.Project{Key: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Upsert(ctx, domain.UpsertInput{Scope: "project", ProjectKey: "alpha", Type: "fact", Title: "viewable", Content: "body", SourceAgent: "test"}); err != nil {
		t.Fatal(err)
	}
	var options []Option
	if enabled {
		options = append(options, WithViewer(db))
	}
	a, b := mcp.NewInMemoryTransports()
	ss, err := New(svc, 8000, options...).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, db
}

func TestViewToolIsOffByDefault(t *testing.T) {
	cs, _ := viewSession(t, false)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 6 {
		t.Fatalf("default tool set changed: %d %v", len(tools.Tools), err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "memory_view" {
			t.Fatal("memory_view registered without opt-in")
		}
	}
}

func TestViewToolWritesSnapshotAndReturnsPath(t *testing.T) {
	cs, db := viewSession(t, true)
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("expected seven tools with the viewer: %d %v", len(tools.Tools), err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "memory_view" && (!strings.Contains(tool.Description, "untrusted stored data") || !strings.Contains(tool.Description, "unauthenticated claims")) {
			t.Fatalf("memory_view lacks the untrusted-data contract: %s", tool.Description)
		}
	}
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_view", Arguments: map[string]any{"project_key": "alpha"}})
	if err != nil || r.IsError {
		t.Fatalf("call failed: %+v %v", r, err)
	}
	raw, _ := json.Marshal(r.StructuredContent)
	var out struct {
		Path     string `json:"path"`
		Projects int    `json:"projects"`
		Memories int    `json:"memories"`
	}
	if err = json.Unmarshal(raw, &out); err != nil || out.Projects != 1 || out.Memories != 1 {
		t.Fatalf("unexpected result %s %v", raw, err)
	}
	if filepath.Dir(out.Path) != filepath.Join(filepath.Dir(db), "views") {
		t.Fatalf("snapshot escaped the views directory: %s", out.Path)
	}
	info, err := os.Stat(out.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot missing or not private: %v %v", info, err)
	}
	if strings.Contains(string(raw), "viewable") {
		t.Fatal("tool result must not echo memory content")
	}
}

func TestViewToolRejectsBadProject(t *testing.T) {
	cs, db := viewSession(t, true)
	for _, key := range []string{"missing", "../escape", "A B"} {
		r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "memory_view", Arguments: map[string]any{"project_key": key}})
		if err != nil || !r.IsError {
			t.Fatalf("%q accepted: %+v %v", key, r, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(filepath.Dir(db), "views")); len(entries) != 0 {
		t.Fatalf("a rejected call left %d files", len(entries))
	}
}
