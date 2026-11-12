package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

func TestProjectsPaginationErrorsIdentifyField(t *testing.T) {
	svc, _ := setup(t)
	for _, tc := range []struct {
		name  string
		in    domain.ProjectsInput
		field string
	}{
		{"negative limit", domain.ProjectsInput{Limit: -1}, "limit"},
		{"excessive limit", domain.ProjectsInput{Limit: 21}, "limit"},
		{"invalid cursor", domain.ProjectsInput{After: "/tmp/project"}, "after"},
		{"oversized cursor", domain.ProjectsInput{After: strings.Repeat("a", 81)}, "after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Projects(context.Background(), tc.in)
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != "validation" || !strings.Contains(failure.Message, tc.field) {
				t.Fatalf("expected actionable %s validation error, got %v", tc.field, err)
			}
		})
	}
}

func TestRegisterThroughProjects(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	reg := func(key, path string) (domain.ProjectsResult, error) {
		return svc.Projects(ctx, domain.ProjectsInput{Register: &domain.Project{Key: key, LocalPath: path}})
	}
	first, err := reg("reg-a", "/work/reg-a")
	if err != nil || len(first.Projects) != 1 || first.Projects[0].Key != "reg-a" || first.Projects[0].CreatedAt == "" {
		t.Fatalf("register failed: %+v %v", first, err)
	}
	again, err := reg("reg-a", "/work/reg-a")
	if err != nil || again.Projects[0].CreatedAt != first.Projects[0].CreatedAt {
		t.Fatalf("identical registration must succeed unchanged: %+v %v", again, err)
	}
	for name, tc := range map[string]struct {
		in   domain.ProjectsInput
		code string
	}{
		"different path for key":  {domain.ProjectsInput{Register: &domain.Project{Key: "reg-a", LocalPath: "/work/other"}}, "conflict"},
		"path owned by other key": {domain.ProjectsInput{Register: &domain.Project{Key: "reg-b", LocalPath: "/work/reg-a"}}, "conflict"},
		"missing path":            {domain.ProjectsInput{Register: &domain.Project{Key: "reg-c"}}, "validation"},
		"relative path":           {domain.ProjectsInput{Register: &domain.Project{Key: "reg-c", LocalPath: "rel"}}, "validation"},
		"mixed with after":        {domain.ProjectsInput{After: "reg-a", Register: &domain.Project{Key: "reg-c", LocalPath: "/work/c"}}, "validation"},
	} {
		_, err := svc.Projects(ctx, tc.in)
		var failure *domain.Error
		if !errors.As(err, &failure) || failure.Code != tc.code {
			t.Fatalf("%s: expected %s, got %v", name, tc.code, err)
		}
	}
}

func TestRegisterNormalizesRepositoryPaths(t *testing.T) {
	svc, store := setup(t)
	ctx := context.Background()
	if err := svc.RegisterProject(ctx, domain.Project{Key: "canonical", LocalPath: "/work/repo/./"}); err != nil {
		t.Fatal(err)
	}
	p, err := store.Project(ctx, "canonical")
	if err != nil || p.LocalPath != "/work/repo" {
		t.Fatalf("noncanonical stored path: %+v %v", p, err)
	}
	if err = svc.RegisterProject(ctx, domain.Project{Key: "canonical", LocalPath: "/work/repo"}); err != nil {
		t.Fatalf("equivalent registration should be unchanged: %v", err)
	}
	for _, path := range []string{"/work/repo/", "/work/repo/.", "/work/other/../repo"} {
		if err = svc.RegisterProject(ctx, domain.Project{Key: "duplicate", LocalPath: path}); code(err) != "conflict" {
			t.Fatalf("path alias %q accepted: %v", path, err)
		}
	}
}

func TestImportRejectsRepositoryPathConflictsAtomically(t *testing.T) {
	for _, withinBatch := range []bool{false, true} {
		t.Run(fmt.Sprint("within_batch=", withinBatch), func(t *testing.T) {
			svc, store := setup(t)
			ctx := context.Background()
			ts := "2026-11-12T00:00:00.000000000Z"
			if !withinBatch {
				if err := svc.RegisterProject(ctx, domain.Project{Key: "existing", LocalPath: "/work/repo"}); err != nil {
					t.Fatal(err)
				}
			}
			firstPath := "/work/fresh"
			if withinBatch {
				firstPath = "/work/repo/"
			}
			data := domain.Export{FormatVersion: 1, ExportedAt: ts, Projects: []domain.Project{{Key: "fresh", LocalPath: firstPath, CreatedAt: ts}, {Key: "duplicate", LocalPath: "/work/repo/./", CreatedAt: ts}}}
			if err := svc.Import(ctx, data); code(err) != "conflict" {
				t.Fatalf("expected path conflict: %v", err)
			}
			if err := store.ProjectExists(ctx, "fresh"); code(err) != "not_found" {
				t.Fatalf("partial import survived: %v", err)
			}
		})
	}
}

func TestImportNormalizesProjectPathsAndRemainsIdempotent(t *testing.T) {
	svc, store := setup(t)
	ctx := context.Background()
	ts := "2026-11-12T00:00:00.000000000Z"
	data := domain.Export{FormatVersion: 1, ExportedAt: ts, Projects: []domain.Project{{Key: "imported", LocalPath: "/work/imported/./", CreatedAt: ts}}}
	for i := 0; i < 2; i++ {
		if err := svc.Import(ctx, data); err != nil {
			t.Fatal(err)
		}
	}
	p, err := store.Project(ctx, "imported")
	if err != nil || p.LocalPath != "/work/imported" || p.CreatedAt != ts {
		t.Fatalf("import changed identity or did not normalize: %+v %v", p, err)
	}
}

func TestConcurrentRegistrationRejectsEquivalentPaths(t *testing.T) {
	svc, store := setup(t)
	other, err := sqlite.Open(store.Path, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	second := New(other, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	results := make(chan error, 2)
	go func() {
		results <- svc.RegisterProject(context.Background(), domain.Project{Key: "concurrent-a", LocalPath: "/work/concurrent"})
	}()
	go func() {
		results <- second.RegisterProject(context.Background(), domain.Project{Key: "concurrent-b", LocalPath: "/work/concurrent/."})
	}()
	wins := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if code(err) != "conflict" {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("expected one registration winner, got %d", wins)
	}
}
