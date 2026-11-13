package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

func TestHelpAndOperationalCommands(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	for _, args := range [][]string{{"--help"}, {"serve", "--help"}, {"doctor", "--help"}, {"status", "--help"}, {"backup", "--help"}, {"export", "--help"}, {"import", "--help"}, {"projects", "--help"}} {
		var out, diagnostics bytes.Buffer
		if code := Run(context.Background(), args, &out, &diagnostics); code != 0 || out.Len() == 0 {
			t.Fatalf("help %v %d %s", args, code, diagnostics.String())
		}
	}
	for _, args := range [][]string{{"projects", "register", "--key", "alpha"}, {"doctor"}, {"status"}, {"projects", "list"}, {"backup", "--output", filepath.Join(dir, "backups", "test.db")}, {"export", "--output", filepath.Join(dir, "exports", "test.json")}, {"import", "--input", filepath.Join(dir, "exports", "test.json")}} {
		var out, diagnostics bytes.Buffer
		if code := Run(context.Background(), args, &out, &diagnostics); code != 0 {
			t.Fatalf("%v %d %s", args, code, diagnostics.String())
		}
	}
	var out, diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"invalid"}, &out, &diagnostics); code != 2 || !strings.Contains(diagnostics.String(), "unknown command") {
		t.Fatalf("invalid: %d %s", code, diagnostics.String())
	}
}

func TestFlagsOverrideEnvironmentAndRejectExplicitZero(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("AGENT_MEMORY_SEARCH_LIMIT", "21")
	var out, diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--search-limit", "5"}, &out, &diagnostics); code != 0 {
		t.Fatalf("flag did not override environment: %d %s", code, diagnostics.String())
	}
	t.Setenv("AGENT_MEMORY_SEARCH_LIMIT", "5")
	out.Reset()
	diagnostics.Reset()
	if code := Run(context.Background(), []string{"status", "--search-limit", "0"}, &out, &diagnostics); code != 2 {
		t.Fatalf("explicit invalid zero accepted: %d", code)
	}
}

func TestDoctorScanReportsIDsOnly(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	var out, diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"projects", "register", "--key", "alpha"}, &out, &diagnostics); code != 0 {
		t.Fatalf("register: %d %s", code, diagnostics.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"doctor", "--scan"}, &out, &diagnostics); code != 0 {
		t.Fatalf("doctor --scan: %d %s", code, diagnostics.String())
	}
	for _, want := range []string{`"audit"`, `"checked":1`, `"flagged_memories":[]`, `"flagged_projects":[]`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
	out.Reset()
	if code := Run(context.Background(), []string{"doctor"}, &out, &diagnostics); code != 0 || strings.Contains(out.String(), "audit") {
		t.Fatalf("plain doctor must not scan: %d %s", code, out.String())
	}
}

func TestDoctorScanExitsNonZeroOnFlaggedRecords(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".local/share/agent-memory/memory.db")
	store, err := sqlite.Open(path, 1000)
	if err != nil {
		t.Fatal(err)
	}
	flagged := domain.Memory{ID: "00000000-0000-4000-8000-0000000000aa", Scope: "global", Type: "fact", Title: "Old", Content: "GITHUB_TOKEN=" + strings.Repeat("a1", 13), Tags: []string{}, SourceAgent: "test", Status: "active", Evidence: "observation"}
	if _, err = store.Put(context.Background(), flagged, 0, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	store.Close()
	var out, diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"doctor", "--scan"}, &out, &diagnostics); code != 1 {
		t.Fatalf("flagged records must exit 1, got %d %s", code, diagnostics.String())
	}
	if !strings.Contains(out.String(), flagged.ID) || strings.Contains(out.String(), "a1a1") {
		t.Fatalf("report must list the ID and never the content: %s", out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"doctor"}, &out, &diagnostics); code != 0 {
		t.Fatalf("plain doctor must stay informational about content: %d", code)
	}
}

func TestViewWritesPrivateSnapshot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	run := func(args ...string) (int, string, string) {
		var out, diagnostics bytes.Buffer
		code := Run(context.Background(), args, &out, &diagnostics)
		return code, out.String(), diagnostics.String()
	}
	if code, _, d := run("projects", "register", "--key", "alpha"); code != 0 {
		t.Fatalf("register: %d %s", code, d)
	}
	target := filepath.Join(dir, "views", "one.html")
	code, out, d := run("view", "--project", "alpha", "--output", target)
	if code != 0 || !strings.Contains(out, target) {
		t.Fatalf("view: %d %s %s", code, out, d)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot missing or not private: %v %v", info, err)
	}
	if code, _, _ = run("view", "--project", "alpha", "--output", target); code != 1 {
		t.Fatalf("existing snapshot overwritten: %d", code)
	}
	if code, _, _ = run("view", "--project", "missing"); code != 1 {
		t.Fatalf("unregistered project accepted: %d", code)
	}
	if code, _, d = run("view", "--help"); code != 0 {
		t.Fatalf("help: %d %s", code, d)
	}
}
