package claudehook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	prompt     = `{"type":"user","message":{"content":"do the thing"}}`
	toolResult = `{"type":"user","message":{"content":[{"type":"tool_result"}]}}`
	bash       = `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`
	search     = `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__agent-memory__memory_search"}]}}`
	upsert     = `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__agent-memory__memory_upsert"}]}}`
	skip       = `{"type":"assistant","message":{"content":[{"type":"text","text":"Memory: no durable change."}]}}`
	sidechain  = `{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"tool_use","name":"mcp__agent-memory__memory_upsert"}]}}`
)

func run(t *testing.T, event string, active bool, last string, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := json.Marshal(map[string]any{"hook_event_name": event, "transcript_path": path, "stop_hook_active": active, "last_assistant_message": last})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = Run(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSessionStartInjectsLoop(t *testing.T) {
	if out := run(t, "SessionStart", false, ""); !strings.Contains(out, "additionalContext") || !strings.Contains(out, "ToolSearch") {
		t.Fatalf("got %q", out)
	}
}

func TestPromptRemindsUntilSearched(t *testing.T) {
	if out := run(t, "UserPromptSubmit", false, "", prompt); !strings.Contains(out, "No memory_search") {
		t.Fatalf("expected reminder, got %q", out)
	}
	if out := run(t, "UserPromptSubmit", false, "", prompt, search, prompt); out != "" {
		t.Fatalf("expected silence after search, got %q", out)
	}
}

func TestStopGate(t *testing.T) {
	cases := []struct {
		name   string
		active bool
		last   string
		lines  []string
		block  bool
	}{
		{"work without write blocks", false, "", []string{prompt, bash, toolResult}, true},
		{"chat only passes", false, "", []string{prompt}, false},
		{"upsert passes", false, "", []string{prompt, bash, upsert}, false},
		{"skip marker in transcript passes", false, "", []string{prompt, bash, skip}, false},
		{"skip marker in last message passes", false, "Memory: no durable change", []string{prompt, bash}, false},
		{"stop_hook_active never loops", true, "", []string{prompt, bash}, false},
		{"earlier turn write does not count", false, "", []string{prompt, upsert, prompt, bash}, true},
		{"subagent write does not count", false, "", []string{prompt, bash, sidechain}, true},
	}
	for _, c := range cases {
		if got := strings.Contains(run(t, "Stop", c.active, c.last, c.lines...), `"block"`); got != c.block {
			t.Errorf("%s: block=%v want %v", c.name, got, c.block)
		}
	}
}

func TestFailsOpen(t *testing.T) {
	var out bytes.Buffer
	if err := Run(strings.NewReader("not json"), &out); err != nil || out.Len() != 0 {
		t.Fatalf("bad payload: %v %q", err, out.String())
	}
	in := `{"hook_event_name":"Stop","transcript_path":"/nonexistent/t.jsonl"}`
	if err := Run(strings.NewReader(in), &out); err != nil || out.Len() != 0 {
		t.Fatalf("missing transcript: %v %q", err, out.String())
	}
}
