package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
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
