package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"
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

func viewSession(t *testing.T, parent string, maxResponseChars int) (*mcp.ClientSession, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, parent, "memory.db")
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
	a, b := mcp.NewInMemoryTransports()
	ss, err := New(svc, maxResponseChars, WithViewer(db)).Connect(ctx, a, nil)
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

func TestViewToolReturnsBrowserAddress(t *testing.T) {
	cs, db := viewSession(t, "private # % ?", 1024)
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("expected six core tools plus memory_view: %d %v", len(tools.Tools), err)
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
	type response struct {
		Path     string `json:"path"`
		FileURL  string `json:"file_url"`
		Projects int    `json:"projects"`
		Memories int    `json:"memories"`
	}
	var out response
	if err = json.Unmarshal(raw, &out); err != nil || out.Projects != 1 || out.Memories != 1 {
		t.Fatalf("unexpected result %s %v", raw, err)
	}
	if filepath.Dir(out.Path) != filepath.Join(filepath.Dir(db), "views") {
		t.Fatalf("snapshot escaped the views directory: %s", out.Path)
	}
	u, err := url.Parse(out.FileURL)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.Path != filepath.ToSlash(out.Path) || u.RawQuery != "" || u.Fragment != "" {
		t.Fatalf("file_url %q does not open path %q: %v", out.FileURL, out.Path, err)
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected one text block: %v", r.Content)
	}
	var textOut response
	textBlock, ok := r.Content[0].(*mcp.TextContent)
	if !ok || json.Unmarshal([]byte(textBlock.Text), &textOut) != nil || textOut != out {
		t.Fatalf("text response does not mirror structured response: %v", r.Content)
	}
	info, err := os.Stat(out.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot missing or not private: %v %v", info, err)
	}
	if strings.Contains(string(raw), "viewable") {
		t.Fatal("tool result must not echo memory content")
	}
}

func TestViewToolKeepsPathWithinResponseBudget(t *testing.T) {
	cs, _ := viewSession(t, strings.Repeat("a", 150), 1024)
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "memory_view"})
	if err != nil || r.IsError {
		t.Fatalf("call failed: %+v %v", r, err)
	}
	out := r.StructuredContent.(map[string]any)
	path, _ := out["path"].(string)
	if path == "" || out["file_url"] != nil {
		t.Fatalf("expected a path without the file URL: %v", out)
	}
}

func TestViewToolRejectsBadProject(t *testing.T) {
	cs, db := viewSession(t, "private", 8000)
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
