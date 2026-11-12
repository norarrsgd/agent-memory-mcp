package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryInvocationAndErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(dir, "private", "db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := New(memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 8000}), 8000)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 6 {
		t.Fatalf("tools %+v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "memory_upsert" {
			continue
		}
		schema, _ := json.Marshal(tool.InputSchema)
		if !bytes.Contains(schema, []byte("RFC3339")) || !strings.Contains(tool.Description, "last_verified_at") {
			t.Fatalf("evidence guidance missing: %s %s", tool.Description, schema)
		}
	}
	args := map[string]any{"scope": "global", "type": "fact", "title": "Protocol test", "content": "SQLite safe", "source_agent": "test"}
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_upsert", Arguments: args})
	if err != nil || r.IsError || r.StructuredContent == nil {
		t.Fatalf("call %+v %v", r, err)
	}
	bts, _ := json.Marshal(r.StructuredContent)
	var created struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(bts, &created); err != nil || created.ID == "" {
		t.Fatalf("invalid output %s", bts)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_get", Arguments: map[string]any{"id": created.ID, "max_response_chars": 1024}})
	if err != nil || r.IsError {
		t.Fatalf("get %+v %v", r, err)
	}
	wire, _ := json.Marshal(r)
	if len([]rune(string(wire))) > 1024 {
		t.Fatal("wire budget exceeded")
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_get", Arguments: map[string]any{"id": "invalid"}})
	if err != nil || !r.IsError || r.StructuredContent == nil {
		t.Fatalf("error %+v %v", r, err)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_get", Arguments: map[string]any{"id": 42}})
	if err == nil && (r == nil || !r.IsError) {
		t.Fatal("schema validation missing")
	}
}

func TestLatestHandoffMinimalInput(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(dir, "private", "db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	if err = svc.RegisterProject(ctx, domain.Project{Key: "alpha"}); err != nil {
		t.Fatal(err)
	}
	server := New(svc, 8000)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_handoff", Arguments: map[string]any{"operation": "latest", "project_key": "alpha"}})
	if err != nil {
		t.Fatalf("minimal latest input rejected by schema: %v", err)
	}
	raw, _ := json.Marshal(r.StructuredContent)
	if !r.IsError || !bytes.Contains(raw, []byte("not_found")) {
		t.Fatalf("expected domain not_found: %s", raw)
	}
}

func TestDocumentedExamples(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(dir, "private", "db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	if err = svc.RegisterProject(ctx, domain.Project{Key: "my-app"}); err != nil {
		t.Fatal(err)
	}
	server := New(svc, 8000)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "example-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	data, err := os.ReadFile("../../examples/memories.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples []struct {
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if err = json.Unmarshal(data, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: example.Tool, Arguments: example.Arguments})
		if err != nil || r.IsError {
			t.Fatalf("example %s rejected: %+v %v", example.Tool, r, err)
		}
	}
}
