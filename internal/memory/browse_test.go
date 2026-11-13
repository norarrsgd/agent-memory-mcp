package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

func seedBrowse(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	for i, kind := range []string{"fact", "fact", "decision"} {
		in := input()
		in.Type = kind
		in.Title = fmt.Sprintf("alpha %s %d", kind, i)
		if _, err := svc.Upsert(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	expired := input()
	expired.Title = "alpha expired"
	expired.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := svc.Upsert(ctx, expired); err != nil {
		t.Fatal(err)
	}
	archived := input()
	archived.Title = "alpha archived"
	a, err := svc.Upsert(ctx, archived)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Archive(ctx, domain.ArchiveInput{ID: a.ID, ExpectedVersion: a.Version}); err != nil {
		t.Fatal(err)
	}
	for task, status := range map[string]string{"t-blocked": "blocked", "t-done": "completed"} {
		if _, err = svc.Handoff(ctx, domain.HandoffInput{Operation: "create", ProjectKey: "alpha", SourceAgent: "codex", Handoff: domain.Handoff{TaskIdentifier: task, Status: status, Summary: "handoff " + task}}); err != nil {
			t.Fatal(err)
		}
	}
	other := input()
	other.ProjectKey = "beta"
	other.Title = "beta fact"
	if _, err = svc.Upsert(ctx, other); err != nil {
		t.Fatal(err)
	}
	global := input()
	global.Scope = "global"
	global.ProjectKey = ""
	global.Type = "preference"
	global.Title = "global preference"
	if _, err = svc.Upsert(ctx, global); err != nil {
		t.Fatal(err)
	}
}

func TestSummaryCountsByTypeStatusAndHandoff(t *testing.T) {
	svc, _ := setup(t)
	seedBrowse(t, svc)
	ctx := context.Background()
	got, err := svc.Summary(ctx, domain.SummaryInput{Scope: "project", ProjectKey: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 5 || got.ByType["fact"] != 2 || got.ByType["decision"] != 1 || got.ByType["handoff"] != 2 {
		t.Fatalf("active counts wrong: %+v", got)
	}
	if got.ByStatus["active"] != 5 || got.ByStatus["archived"] != 1 || got.ByStatus["expired"] != 1 || got.ByStatus["superseded"] != 0 {
		t.Fatalf("status counts wrong: %+v", got.ByStatus)
	}
	if got.ByHandoffStatus["blocked"] != 1 || got.ByHandoffStatus["completed"] != 1 {
		t.Fatalf("handoff counts wrong: %+v", got.ByHandoffStatus)
	}
	global, err := svc.Summary(ctx, domain.SummaryInput{Scope: "global"})
	if err != nil || global.Total != 1 || global.ByType["preference"] != 1 {
		t.Fatalf("global summary leaked or missed records: %+v %v", global, err)
	}
	project, err := svc.store.Project(ctx, "alpha")
	if err != nil || project.MemoryCount != got.Total {
		t.Fatalf("summary total must match memory_projects count: %d vs %+v %v", got.Total, project, err)
	}
}

func TestListFiltersAreScopedAndExact(t *testing.T) {
	svc, _ := setup(t)
	seedBrowse(t, svc)
	ctx := context.Background()
	titles := func(in domain.ListInput) []string {
		t.Helper()
		in.Scope, in.ProjectKey = "project", "alpha"
		out, err := svc.List(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, item := range out.Items {
			names = append(names, item.Title)
		}
		return names
	}
	if got := titles(domain.ListInput{}); len(got) != 5 {
		t.Fatalf("default list must be active and unexpired: %v", got)
	}
	if got := titles(domain.ListInput{Type: "decision"}); len(got) != 1 || got[0] != "alpha decision 2" {
		t.Fatalf("type filter: %v", got)
	}
	if got := titles(domain.ListInput{Status: "expired"}); len(got) != 1 || got[0] != "alpha expired" {
		t.Fatalf("expired filter: %v", got)
	}
	if got := titles(domain.ListInput{Status: "archived"}); len(got) != 1 || got[0] != "alpha archived" {
		t.Fatalf("archived filter: %v", got)
	}
	if got := titles(domain.ListInput{HandoffStatus: "blocked"}); len(got) != 1 || !strings.Contains(got[0], "t-blocked") {
		t.Fatalf("handoff filter: %v", got)
	}
	for _, name := range titles(domain.ListInput{Limit: 50}) {
		if strings.HasPrefix(name, "beta") || strings.HasPrefix(name, "global") {
			t.Fatalf("project list leaked %q", name)
		}
	}
	global, err := svc.List(ctx, domain.ListInput{Scope: "global"})
	if err != nil || len(global.Items) != 1 || global.Items[0].Title != "global preference" {
		t.Fatalf("global list: %+v %v", global, err)
	}
	for _, item := range global.Items {
		if item.Evidence == "" || item.Status != "active" || item.Version != 1 {
			t.Fatalf("reference metadata missing: %+v", item)
		}
	}
}

func TestListCursorWalksEveryRecordOnce(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		in := input()
		in.Title = fmt.Sprintf("record %d", i)
		if _, err := svc.Upsert(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	after := ""
	for pages := 0; ; pages++ {
		out, err := svc.List(ctx, domain.ListInput{Scope: "project", ProjectKey: "alpha", Limit: 3, After: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range out.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate %s across pages", item.ID)
			}
			seen[item.ID] = true
		}
		if out.NextCursor == "" {
			if pages != 2 {
				t.Fatalf("expected 3 pages, got %d", pages+1)
			}
			break
		}
		after = out.NextCursor
	}
	if len(seen) != 7 {
		t.Fatalf("walked %d of 7 records", len(seen))
	}
}

func TestBrowseRejectsInvalidInput(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for name, in := range map[string]domain.ListInput{
		"missing scope":         {},
		"unknown scope":         {Scope: "all"},
		"global with project":   {Scope: "global", ProjectKey: "alpha"},
		"unregistered project":  {Scope: "project", ProjectKey: "nope"},
		"bad project key":       {Scope: "project", ProjectKey: "../x"},
		"bad status":            {Scope: "global", Status: "deleted"},
		"bad type":              {Scope: "global", Type: "note"},
		"bad handoff status":    {Scope: "global", HandoffStatus: "open"},
		"handoff status + type": {Scope: "global", Type: "fact", HandoffStatus: "blocked"},
		"negative limit":        {Scope: "global", Limit: -1},
		"excess limit":          {Scope: "global", Limit: 51},
		"garbage cursor":        {Scope: "global", After: "/tmp/x"},
		"cursor bad id":         {Scope: "global", After: "2026-11-13T00:00:00.000000000Z~nope"},
	} {
		if _, err := svc.List(ctx, in); code(err) != "validation" {
			t.Errorf("%s: expected validation error, got %v", name, err)
		}
	}
	if _, err := svc.Summary(ctx, domain.SummaryInput{Scope: "project", ProjectKey: "nope"}); code(err) != "validation" {
		t.Errorf("summary of unregistered project: got %v", err)
	}
}
