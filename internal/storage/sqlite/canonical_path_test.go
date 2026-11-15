package sqlite

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

func requireValidation(t *testing.T, err error, what string) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != "validation" {
		t.Fatalf("%s: want validation error, got %v", what, err)
	}
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPrivatePathRejectsNoncanonicalSpellings(t *testing.T) {
	sep := string(filepath.Separator)
	base := privateTempDir(t)
	cases := map[string]string{
		"dot segment":       base + sep + "." + sep + "x.db",
		"dot dot segment":   base + sep + "sub" + sep + ".." + sep + "x.db",
		"repeated sep":      base + sep + sep + "x.db",
		"trailing sep":      base + sep + "x.db" + sep,
		"trailing dot":      base + sep + "x.db" + sep + ".",
		"missing dot dot":   base + sep + "missing" + sep + ".." + sep + "x.db",
		"missing dir/dots":  base + sep + "new" + sep + "." + sep + "x.db",
		"leading repeated":  sep + base + sep + "x.db",
		"trailing dir seps": base + sep + "new" + sep + sep,
	}
	for name, path := range cases {
		for _, create := range []bool{false, true} {
			requireValidation(t, PrivatePath(path, create), name)
		}
		if got := entryNames(t, base); len(got) != 0 {
			t.Fatalf("%s created entries: %v", name, got)
		}
	}
	if err := PrivatePath(filepath.Join(base, "ok.db"), true); err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
}

// escapeFixture builds base/link -> outside/inner so that base/link/../output lexically
// cleans to base/output but the operating system resolves it to outside/output.
func escapeFixture(t *testing.T) (base, outside, spelled, sentinel string) {
	t.Helper()
	root := privateTempDir(t)
	base = filepath.Join(root, "base")
	outside = filepath.Join(root, "outside")
	for _, d := range []string{base, outside, filepath.Join(outside, "inner")} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "inner"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	sentinel = filepath.Join(outside, "output")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	spelled = base + sep + "link" + sep + ".." + sep + "output"
	return
}

func assertUntouched(t *testing.T, base, outside, sentinel string) {
	t.Helper()
	b, err := os.ReadFile(sentinel)
	if err != nil || string(b) != "sentinel" {
		t.Fatalf("sentinel changed: %q %v", b, err)
	}
	if got := entryNames(t, outside); len(got) != 2 {
		t.Fatalf("outside dir changed: %v", got)
	}
	if got := entryNames(t, filepath.Join(outside, "inner")); len(got) != 0 {
		t.Fatalf("inner dir changed: %v", got)
	}
	if got := entryNames(t, base); len(got) != 1 || got[0] != "link" {
		t.Fatalf("base dir changed: %v", got)
	}
}

func TestSymlinkDotDotEscapeIsRejected(t *testing.T) {
	for _, create := range []bool{false, true} {
		base, outside, spelled, sentinel := escapeFixture(t)
		requireValidation(t, PrivatePath(spelled, create), "PrivatePath")
		assertUntouched(t, base, outside, sentinel)
	}
}

func TestWritePrivateFileRejectsNoncanonicalWithoutWriter(t *testing.T) {
	base, outside, spelled, sentinel := escapeFixture(t)
	called := false
	err := WritePrivateFile(spelled, func(io.Writer) error { called = true; return nil })
	requireValidation(t, err, "WritePrivateFile")
	if called {
		t.Fatal("writer callback invoked")
	}
	assertUntouched(t, base, outside, sentinel)
}

func TestOpenRejectsNoncanonicalPath(t *testing.T) {
	base, outside, spelled, sentinel := escapeFixture(t)
	if s, err := Open(spelled, 100); err == nil {
		s.Close()
		t.Fatal("Open accepted path")
	} else {
		requireValidation(t, err, "Open")
	}
	if s, err := OpenLimited(spelled, 100, 16); err == nil {
		s.Close()
		t.Fatal("OpenLimited accepted path")
	} else {
		requireValidation(t, err, "OpenLimited")
	}
	assertUntouched(t, base, outside, sentinel)
}

func TestBackupRejectsNoncanonicalDestination(t *testing.T) {
	dir := privateTempDir(t)
	s, err := Open(filepath.Join(dir, "memory.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before := entryNames(t, dir)
	base, outside, spelled, sentinel := escapeFixture(t)
	requireValidation(t, s.Backup(context.Background(), spelled), "Backup")
	assertUntouched(t, base, outside, sentinel)
	sep := string(filepath.Separator)
	requireValidation(t, s.Backup(context.Background(), dir+sep+"."+sep+"b.db"), "Backup dot")
	if got := entryNames(t, dir); len(got) != len(before) {
		t.Fatalf("backup created artifacts: before %v after %v", before, got)
	}
}
