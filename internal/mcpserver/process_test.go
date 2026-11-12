package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/norarrsgd/agent-memory-mcp/internal/cli"
)

func TestMCPProcess(t *testing.T) {
	if os.Getenv("AGENT_MEMORY_TEST_PROCESS") != "1" {
		return
	}
	code := cli.Run(context.Background(), []string{"serve", "--db-path", os.Getenv("AGENT_MEMORY_TEST_DB")}, os.Stdout, os.Stderr)
	os.Exit(code)
}
func TestSeparateProcessesSharedDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "private", "memory.db")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type process struct {
		session *mcp.ClientSession
		command *exec.Cmd
		stderr  *bytes.Buffer
	}
	connect := func() process {
		cmd := exec.Command(executable, "-test.run=^TestMCPProcess$")
		cmd.Env = append(os.Environ(), "AGENT_MEMORY_TEST_PROCESS=1", "AGENT_MEMORY_TEST_DB="+db)
		diagnostics := new(bytes.Buffer)
		cmd.Stderr = diagnostics
		client := mcp.NewClient(&mcp.Implementation{Name: "process-test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatalf("connect %v %s", err, diagnostics.String())
		}
		t.Cleanup(func() { session.Close() })
		return process{session, cmd, diagnostics}
	}
	a := connect()
	b := connect()
	call := func(session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := call(a.session, "memory_upsert", map[string]any{"scope": "global", "type": "fact", "title": "Cross process fact", "content": "SQLite shared WAL state", "source_agent": "codex"})
	if r.IsError {
		t.Fatalf("write %+v", r)
	}
	raw, _ := json.Marshal(r.StructuredContent)
	var write struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if err = json.Unmarshal(raw, &write); err != nil {
		t.Fatal(err)
	}
	r = call(b.session, "memory_get", map[string]any{"id": write.ID})
	if r.IsError {
		t.Fatal("cross-process read failed")
	}
	// Independent OS processes contend on the same version. Exactly one may win.
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, session := range []*mcp.ClientSession{a.session, b.session} {
		wg.Add(1)
		go func(session *mcp.ClientSession) {
			defer wg.Done()
			r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "memory_upsert", Arguments: map[string]any{"id": write.ID, "expected_version": 1, "scope": "global", "type": "fact", "title": "Cross process fact", "content": "updated concurrent state", "source_agent": "claude"}})
			if err != nil {
				results <- false
				return
			}
			results <- !r.IsError
		}(session)
	}
	wg.Wait()
	close(results)
	wins := 0
	for won := range results {
		if won {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("expected one CAS winner, got %d", wins)
	}
	// Kill one process, then verify the surviving process and a fresh process can read.
	if err = a.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	a.session.Close()
	r = call(b.session, "memory_search", map[string]any{"query": "concurrent"})
	if r.IsError {
		t.Fatal("search after termination failed")
	}
	c := connect()
	r = call(c.session, "memory_get", map[string]any{"id": write.ID})
	if r.IsError {
		t.Fatal("restart lost committed data")
	}
}
