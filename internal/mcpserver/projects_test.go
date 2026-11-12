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
