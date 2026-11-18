package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

func TestFindingAndDiscoveryTypes(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for _, kind := range []string{"finding", "discovery"} {
		in := input()
		in.Type = kind
		in.Title = "zebra " + kind
		in.Content = "zebra stripes are unique " + kind
		if _, err := svc.Upsert(ctx, in); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		r, err := svc.Search(ctx, domain.SearchInput{Query: "zebra", ProjectKey: "alpha", MemoryTypes: []string{kind}})
		if err != nil || len(r.Results) != 1 || r.Results[0].Type != kind {
			t.Fatalf("%s search %+v %v", kind, r, err)
		}
	}
	if r, err := svc.Search(ctx, domain.SearchInput{Query: "zebra", ProjectKey: "alpha", MemoryTypes: []string{"finding", "discovery"}}); err != nil || len(r.Results) != 2 {
		t.Fatalf("combined filter %+v %v", r, err)
	}
	in := input()
	in.Type = "finding"
	in.Evidence = "approved_decision"
	in.SourceReference = "ref"
	if _, err := svc.Upsert(ctx, in); err == nil || !strings.Contains(err.Error(), "decision") {
		t.Fatalf("approved_decision must stay decision-only: %v", err)
	}
}

// TestInvalidTypeErrorsListAcceptedTypes keeps every validation path naming the full accepted set.
func TestInvalidTypeErrorsListAcceptedTypes(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	in := input()
	in.Type = "insight"
	_, upsertErr := svc.Upsert(ctx, in)
	_, searchErr := svc.Search(ctx, domain.SearchInput{Query: "zebra", MemoryTypes: []string{"insight"}})
	_, listErr := svc.List(ctx, domain.ListInput{Scope: "global", Type: "insight"})
	for name, err := range map[string]error{"upsert": upsertErr, "search": searchErr, "list": listErr} {
		if code(err) != "validation" {
			t.Fatalf("%s: expected validation error, got %v", name, err)
		}
		for _, kind := range domain.MemoryTypes {
			if !strings.Contains(err.Error(), kind) {
				t.Fatalf("%s error omits %q: %v", name, kind, err)
			}
		}
	}
	in.Type = "handoff"
	if _, err := svc.Upsert(ctx, in); code(err) != "validation" || !strings.Contains(err.Error(), "memory_handoff") {
		t.Fatalf("handoff upsert: %v", err)
	}
	if _, err := svc.Search(ctx, domain.SearchInput{Query: "zebra", MemoryTypes: append(append([]string{}, domain.MemoryTypes...), "fact")}); code(err) != "validation" {
		t.Fatalf("expected too-many-types error: %v", err)
	}
}
