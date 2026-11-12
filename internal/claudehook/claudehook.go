// Package claudehook implements the optional Claude Code hook that makes the shared-memory loop enforceable.
// It is stateless: it reads the hook payload and the session transcript, never the memory database, and fails open.
package claudehook

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

const (
	toolPrefix = "mcp__agent-memory__"
	// SkipMarker is the one-line statement an agent writes when nothing durable changed; it satisfies the Stop gate.
	SkipMarker = "memory: no durable change"

	startReminder = "Shared memory loop: resolve this repository with memory_projects, then run one memory_search for the task topic and memory_get only relevant IDs. " +
		"The mcp__agent-memory__* tools are deferred; load them first with ToolSearch (select:mcp__agent-memory__memory_projects,mcp__agent-memory__memory_search,mcp__agent-memory__memory_get,mcp__agent-memory__memory_upsert,mcp__agent-memory__memory_handoff). " +
		"Before the final response of any implementation, investigation or design task, memory_upsert durable findings, or state \"Memory: no durable change\" in one line. Before stopping unfinished work, call memory_handoff."
	searchReminder = "No memory_search has run in this session. Resolve the project and search memory for this task's topic before answering (load the deferred mcp__agent-memory__* tools with ToolSearch first)."
	stopReason     = "This turn did work but recorded nothing in shared memory. Search for an existing record then call memory_upsert (or memory_handoff if work is unfinished). " +
		"If nothing durable changed, say \"Memory: no durable change\" in one line instead."
)

type payload struct {
	Event          string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	StopHookActive bool   `json:"stop_hook_active"`
	LastMessage    string `json:"last_assistant_message"`
}

type entry struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Text string `json:"text"`
}

type state struct {
	searched bool // any memory_search in the session
	work     bool // non-memory tool use since the last user prompt
	wrote    bool // memory_upsert or memory_handoff since the last user prompt
	skipped  bool // skip marker in assistant text since the last user prompt
}

// Run handles one hook invocation. Any malformed input yields no output and a nil error so a broken hook never blocks Claude.
func Run(in io.Reader, out io.Writer) error {
	var p payload
	if json.NewDecoder(in).Decode(&p) != nil {
		return nil
	}
	var s state
	if p.Event != "SessionStart" {
		s = scan(p.TranscriptPath)
	}
	var result any
	switch p.Event {
	case "SessionStart":
		result = additionalContext(p.Event, startReminder)
	case "UserPromptSubmit":
		if !s.searched {
			result = additionalContext(p.Event, searchReminder)
		}
	case "Stop":
		skipped := s.skipped || strings.Contains(strings.ToLower(p.LastMessage), SkipMarker)
		if !p.StopHookActive && s.work && !s.wrote && !skipped {
			result = map[string]string{"decision": "block", "reason": stopReason}
		}
	}
	if result == nil {
		return nil
	}
	return json.NewEncoder(out).Encode(result)
}

func additionalContext(event, text string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": text}}
}

func scan(path string) state {
	var s state
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var e entry
		if err = dec.Decode(&e); err != nil {
			return s
		}
		if e.IsSidechain {
			continue
		}
		switch e.Type {
		case "user":
			if !e.IsMeta && isPrompt(e.Message.Content) {
				s.work, s.wrote, s.skipped = false, false, false
			}
		case "assistant":
			var blocks []block
			if json.Unmarshal(e.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch {
				case b.Type == "text":
					s.skipped = s.skipped || strings.Contains(strings.ToLower(b.Text), SkipMarker)
				case b.Type != "tool_use":
				case b.Name == toolPrefix+"memory_search":
					s.searched = true
				case b.Name == toolPrefix+"memory_upsert" || b.Name == toolPrefix+"memory_handoff":
					s.wrote = true
				case !strings.HasPrefix(b.Name, toolPrefix) && b.Name != "ToolSearch":
					s.work = true
				}
			}
		}
	}
}

// isPrompt reports whether user content is a human prompt rather than a tool result.
func isPrompt(content json.RawMessage) bool {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return true
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return len(blocks) > 0
}
