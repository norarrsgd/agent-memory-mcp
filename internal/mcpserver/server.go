// Package mcpserver adapts MCP requests to the independently testable service.
package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
)

func add[In, Out any](server *mcp.Server, tool *mcp.Tool, max int, fn func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		if err := domain.CheckInputSize(in); err != nil {
			return toolError(err)
		}
		value, err := fn(ctx, in)
		if err != nil {
			return toolError(err)
		}
		// Check the actual SDK-compatible result as well as service-level budgets.
		b, err := json.Marshal(value)
		if err != nil {
			return toolError(domain.Err("storage_failure", "cannot encode response"))
		}
		result := &mcp.CallToolResult{StructuredContent: json.RawMessage(b), Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
		encoded, err := json.Marshal(result)
		if err != nil || len([]rune(string(encoded))) > max {
			return toolError(domain.Err("budget_exceeded", "response exceeds configured maximum"))
		}
		return result, nil, nil
	})
}
func toolError(err error) (*mcp.CallToolResult, any, error) {
	value := map[string]any{"error": domain.SafeError(err)}
	b, _ := json.Marshal(value)
	return &mcp.CallToolResult{IsError: true, StructuredContent: json.RawMessage(b), Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}
func New(s *memory.Service, max int) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "agent-memory", Version: "0.1.0"}, nil)
	add(server, &mcp.Tool{Name: "memory_search", Description: "Find compact memory references; retrieve relevant IDs separately."}, max, s.Search)
	add(server, &mcp.Tool{Name: "memory_get", Description: "Retrieve one memory; continue truncated content using offset and version."}, max, s.Get)
	add(server, &mcp.Tool{Name: "memory_upsert", Description: "Create or update memory; updates require expected_version."}, max, s.Upsert)
	add(server, &mcp.Tool{Name: "memory_handoff", Description: "Create, update, or find the latest active project handoff."}, max, s.Handoff)
	add(server, &mcp.Tool{Name: "memory_archive", Description: "Archive an active memory using expected_version."}, max, s.Archive)
	add(server, &mcp.Tool{
		Name: "memory_projects", Description: "List projects and active memory counts (call with {} for the first page), or register one via register. Match the repository's absolute path against local_path before registering; identical registration succeeds, a conflicting key or path returns a conflict.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"limit": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 20, "default": 10,
					"description": "Page size, at most 20; omit or use 0 for default 10.",
				},
				"after": map[string]any{
					"type": "string", "maxLength": 80, "pattern": "^([a-z0-9][a-z0-9._-]{0,79})?$",
					"description": "Omit for the first page; otherwise copy next_cursor from the previous response.",
				},
				"register": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"key", "local_path"},
					"description": "Register instead of listing; ignores limit and cannot be combined with after. Returns key, created_at and memory_count.",
					"properties": map[string]any{
						"key":          map[string]any{"type": "string", "maxLength": 80, "pattern": "^[a-z0-9][a-z0-9._-]{0,79}$", "description": "Explicit stable project key; never derive it implicitly."},
						"local_path":   map[string]any{"type": "string", "maxLength": 1024, "description": "Absolute repository path, passed explicitly by the agent."},
						"remote_url":   map[string]any{"type": "string", "maxLength": 512},
						"organization": map[string]any{"type": "string", "maxLength": 160},
						"description":  map[string]any{"type": "string", "maxLength": 1000},
					},
				},
			},
		},
	}, max, s.Projects)
	return server
}
