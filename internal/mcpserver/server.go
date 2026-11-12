// Package mcpserver adapts MCP requests to the independently testable service.
package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
)

func add[In, Out any](server *mcp.Server, name, description string, max int, fn func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
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
	add(server, "memory_search", "Find compact memory references; retrieve relevant IDs separately.", max, s.Search)
	add(server, "memory_get", "Retrieve one memory; continue truncated content using offset and version.", max, s.Get)
	add(server, "memory_upsert", "Create or update memory; updates require expected_version.", max, s.Upsert)
	add(server, "memory_handoff", "Create, update, or find the latest active project handoff.", max, s.Handoff)
	add(server, "memory_archive", "Archive an active memory using expected_version.", max, s.Archive)
	add(server, "memory_projects", "List registered projects and active memory counts.", max, s.Projects)
	return server
}
