package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

func TestProjectsPaginationContract(t *testing.T) {
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
	for _, key := range []string{"alpha", "beta"} {
		if err = svc.RegisterProject(ctx, domain.Project{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	server := New(svc, 8000)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "pagination-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("advertised schema", func(t *testing.T) {
		var tool *mcp.Tool
		for _, candidate := range tools.Tools {
			if candidate.Name == "memory_projects" {
				tool = candidate
				break
			}
		}
		if tool == nil {
			t.Fatal("memory_projects missing")
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Minimum     *int   `json:"minimum"`
				Maximum     *int   `json:"maximum"`
				Default     int    `json:"default"`
				Pattern     string `json:"pattern"`
				MaxLength   int    `json:"maxLength"`
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err = json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		limit := schema.Properties["limit"]
		if limit.Minimum == nil || *limit.Minimum != 0 || limit.Maximum == nil || *limit.Maximum != 20 || limit.Default != 10 {
			t.Fatalf("pagination limits/default not advertised: %s", raw)
		}
		after := schema.Properties["after"]
		if after.Pattern == "" || after.MaxLength != 80 || !strings.Contains(after.Description, "next_cursor") {
			t.Fatalf("cursor contract not advertised: %s", raw)
		}
	})
	for _, tc := range []struct {
		name   string
		args   map[string]any
		count  int
		cursor string
	}{
		{"defaults", map[string]any{}, 2, ""},
		{"zero defaults", map[string]any{"limit": 0}, 2, ""},
		{"first page", map[string]any{"limit": 1}, 1, "alpha"},
		{"next page", map[string]any{"limit": 1, "after": "alpha"}, 1, ""},
		{"maximum", map[string]any{"limit": 20, "after": ""}, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_projects", Arguments: tc.args})
			if err != nil || r.IsError {
				t.Fatalf("valid pagination rejected: %+v %v", r, err)
			}
			raw, _ := json.Marshal(r.StructuredContent)
			var result domain.ProjectsResult
			if err = json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Projects) != tc.count || result.NextCursor != tc.cursor {
				t.Fatalf("unexpected pagination result: %s", raw)
			}
			if tc.name == "next page" && result.Projects[0].Key != "beta" {
				t.Fatal("cursor did not advance")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		args  map[string]any
		field string
	}{
		{"negative", map[string]any{"limit": -1}, "limit"},
		{"too large", map[string]any{"limit": 21}, "limit"},
		{"bad cursor", map[string]any{"after": "/tmp/project"}, "after"},
		{"cursor too long", map[string]any{"after": strings.Repeat("a", 81)}, "after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_projects", Arguments: tc.args})
			if err != nil {
				if !strings.Contains(err.Error(), tc.field) {
					t.Fatalf("schema error did not identify field: %v", err)
				}
				return
			}
			raw, _ := json.Marshal(r)
			if !r.IsError || !strings.Contains(string(raw), tc.field) {
				t.Fatalf("expected actionable %s rejection, got %s", tc.field, raw)
			}
		})
	}
}

func TestProjectsRegisterOverMCP(t *testing.T) {
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
	server := New(memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 8000}), 8000)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "register-test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(path string) *mcp.CallToolResult {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_projects", Arguments: map[string]any{"register": map[string]any{"key": "repo", "local_path": path}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := call("/work/repo"); r.IsError {
		t.Fatalf("register failed: %+v", r)
	}
	if r := call("/work/repo"); r.IsError {
		t.Fatalf("identical registration failed: %+v", r)
	}
	raw, _ := json.Marshal(call("/work/moved"))
	if !strings.Contains(string(raw), `"isError":true`) || !strings.Contains(string(raw), "conflict") {
		t.Fatalf("expected conflict, got %s", raw)
	}
}

func TestProjectRegistrationFitsMinimumResponseBudget(t *testing.T) {
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
	server := New(memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 1024}), 1024)
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "budget-test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	args := map[string]any{"register": map[string]any{"key": strings.Repeat("a", 80), "local_path": "/work/repo/", "description": strings.Repeat("x", 1000)}}
	for i := 0; i < 2; i++ {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "memory_projects", Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("registration committed but acknowledgement failed: %+v %v", result, err)
		}
		wire, _ := json.Marshal(result)
		if len([]rune(string(wire))) > 1024 {
			t.Fatalf("response exceeded budget: %d", len([]rune(string(wire))))
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var response domain.ProjectsResult
		if err = json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Projects) != 1 || response.Projects[0].Key != strings.Repeat("a", 80) || response.Projects[0].CreatedAt == "" {
			t.Fatalf("missing registration acknowledgement: %s", raw)
		}
	}
	stored, err := store.Project(ctx, strings.Repeat("a", 80))
	if err != nil || len(stored.Description) != 1000 {
		t.Fatalf("registration lost metadata: %+v %v", stored, err)
	}
}
