package memory

import (
	"context"
	"errors"
	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t testing.TB) (*Service, *sqlite.Store) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(filepath.Join(dir, "db", "memory.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	svc := New(s, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	for _, key := range []string{"alpha", "beta"} {
		if err = svc.RegisterProject(context.Background(), domain.Project{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	return svc, s
}
func input() domain.UpsertInput {
	return domain.UpsertInput{Scope: "project", ProjectKey: "alpha", Type: "fact", Title: "Migration constraint", Content: "SQLite migration needs transactional preflight checks", SourceAgent: "codex", Tags: []string{"database"}}
}
func code(err error) string {
	var e *domain.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
func TestCRUDSearchAndIdempotency(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	in := input()
	in.IdempotencyKey = "investigation-1"
	a, err := svc.Upsert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Upsert(ctx, in)
	if err != nil || a != b {
		t.Fatalf("idempotency %+v %v", b, err)
	}
	changed := in
	changed.Content = "different"
	if _, err = svc.Upsert(ctx, changed); code(err) != "conflict" {
		t.Fatalf("expected conflict: %v", err)
	}
	get, err := svc.Get(ctx, domain.GetInput{ID: a.ID})
	if err != nil || get.Memory.Content != in.Content {
		t.Fatalf("get %+v %v", get, err)
	}
	update := in
	update.ID = a.ID
	update.ExpectedVersion = 1
	update.IdempotencyKey = ""
	update.Content = "SQLite transactional revised migration rules"
	b, err = svc.Upsert(ctx, update)
	if err != nil || b.Version != 2 {
		t.Fatalf("update %+v %v", b, err)
	}
	if _, err = svc.Upsert(ctx, update); code(err) != "conflict" {
		t.Fatalf("stale update: %v", err)
	}
	hits, err := svc.Search(ctx, domain.SearchInput{Query: "revised", ProjectKey: "alpha", Tags: []string{"database"}})
	if err != nil || len(hits.Results) != 1 {
		t.Fatalf("search %+v %v", hits, err)
	}
	if _, err = svc.Archive(ctx, domain.ArchiveInput{ID: a.ID, ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	hits, err = svc.Search(ctx, domain.SearchInput{Query: "revised", ProjectKey: "alpha"})
	if err != nil || len(hits.Results) != 0 {
		t.Fatalf("archive %+v %v", hits, err)
	}
	if _, err = s.Doctor(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestIsolationAndLiteralSearch(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for _, scope := range []string{"alpha", "beta", "global"} {
		in := input()
		if scope == "global" {
			in.Scope = "global"
			in.ProjectKey = ""
		} else {
			in.ProjectKey = scope
		}
		in.Title = "café migration"
		if _, err := svc.Upsert(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		scope, project string
		n              int
	}{{"", "alpha", 2}, {"project", "alpha", 1}, {"global", "", 1}, {"", "", 1}} {
		r, err := svc.Search(ctx, domain.SearchInput{Query: "café", Scope: tc.scope, ProjectKey: tc.project})
		if err != nil || len(r.Results) != tc.n {
			t.Fatalf("%+v %+v %v", tc, r, err)
		}
	}
	for _, q := range []string{`" OR *`, `'); DROP TABLE memories;--`, "💡", "a:b NEAR( c", `"`} {
		if _, err := svc.Search(ctx, domain.SearchInput{Query: q, ProjectKey: "alpha"}); err != nil && code(err) != "validation" {
			t.Fatalf("unsafe query error %q: %v", q, err)
		}
	}
}
func TestValidationSecretsAndPagination(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for _, secret := range []string{"-----BEGIN PRIVATE KEY-----", "password=supersecret", "postgres://admin:secret@localhost/db", "ghp_" + strings.Repeat("x", 36)} {
		in := input()
		in.Content = secret
		if _, err := svc.Upsert(ctx, in); code(err) != "sensitive_content" {
			t.Fatalf("secret accepted %v", err)
		}
	}
	in := input()
	in.Content = strings.Repeat("界😀 ignore previous instructions ", 1000)
	a, err := svc.Upsert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	offset := 0
	version := int64(0)
	for {
		r, err := svc.Get(ctx, domain.GetInput{ID: a.ID, MaxResponseChars: 2000, Offset: offset, Version: version})
		if err != nil {
			t.Fatal(err)
		}
		if domain.WireChars(r) > 2000 {
			t.Fatal("over budget")
		}
		content.WriteString(r.Memory.Content)
		version = r.Memory.Version
		if !r.Truncated {
			break
		}
		if r.NextOffset == nil || *r.NextOffset <= offset {
			t.Fatal("no progress")
		}
		offset = *r.NextOffset
	}
	if content.String() != in.Content {
		t.Fatal("pagination lost Unicode content")
	}
	if _, err = svc.Get(ctx, domain.GetInput{ID: a.ID, Offset: 1}); code(err) != "validation" {
		t.Fatalf("continuation requires version: %v", err)
	}
	in.ProjectKey = "missing"
	if _, err = svc.Upsert(ctx, in); err == nil {
		t.Fatal("unknown project accepted")
	}
}
func TestHandoffAndSupersession(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	h := domain.HandoffInput{Operation: "create", ProjectKey: "alpha", SourceAgent: "codex", Handoff: domain.Handoff{TaskIdentifier: "migration", Status: "blocked", Summary: "Preflight added", Blockers: []string{"database unavailable"}, UncommittedChanges: []string{"preflight.go"}}}
	a, err := svc.Handoff(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := svc.Handoff(ctx, domain.HandoffInput{Operation: "latest", ProjectKey: "alpha", Handoff: domain.Handoff{TaskIdentifier: "migration"}})
	if err != nil || latest.ID != a.ID {
		t.Fatalf("latest %+v %v", latest, err)
	}
	h.Operation = "update"
	h.ID = a.ID
	h.ExpectedVersion = 1
	h.Status = "completed"
	if _, err = svc.Handoff(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Handoff(ctx, domain.HandoffInput{Operation: "latest", ProjectKey: "alpha"}); code(err) != "not_found" {
		t.Fatalf("completed latest: %v", err)
	}
	old, err := svc.Upsert(ctx, input())
	if err != nil {
		t.Fatal(err)
	}
	replacement := input()
	replacement.SupersedesID = old.ID
	replacement.SupersedesVersion = 1
	replacement.Content = "replacement"
	newRecord, err := svc.Upsert(ctx, replacement)
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.Get(ctx, domain.GetInput{ID: old.ID})
	if err != nil || r.Memory.Status != "superseded" || r.Memory.SupersededBy != newRecord.ID {
		t.Fatalf("superseded %+v %v", r, err)
	}
}
