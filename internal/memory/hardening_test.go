package memory

import (
	"context"
	"encoding/json"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNoopExpiryTypeFilterAndBudget(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	in := input()
	a, err := svc.Upsert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ID = a.ID
	in.ExpectedVersion = 1
	b, err := svc.Upsert(ctx, in)
	if err != nil || b.Version != 1 {
		t.Fatalf("noop changed version: %+v %v", b, err)
	}
	in = input()
	in.Type = "lesson"
	in.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err = svc.Upsert(ctx, in); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Search(ctx, domain.SearchInput{Query: "migration", ProjectKey: "alpha", MemoryTypes: []string{"lesson"}})
	if err != nil || len(r.Results) != 0 {
		t.Fatalf("expired: %+v %v", r, err)
	}
	for i := 0; i < 10; i++ {
		in = input()
		in.Title = strings.Repeat("Migration ", 15)
		in.Content = strings.Repeat("migration ", 100)
		if _, err = svc.Upsert(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	r, err = svc.Search(ctx, domain.SearchInput{Query: "migration", ProjectKey: "alpha", Limit: 20, MaxResponseChars: 1024})
	if err != nil || domain.WireChars(r) > 1024 || !r.BudgetLimited {
		t.Fatalf("search budget: %+v %v", r, err)
	}
}
func TestConcurrentServiceReadWrite(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, err := svc.Upsert(ctx, input()); errs <- err }()
		go func() {
			defer wg.Done()
			_, err := svc.Search(ctx, domain.SearchInput{Query: "migration", ProjectKey: "alpha"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Doctor(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestRejectInvalidInputsAndUnverifiedClaims(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for _, change := range []func(*domain.UpsertInput){func(i *domain.UpsertInput) { i.ID = "not-a-uuid" }, func(i *domain.UpsertInput) { i.Scope = "other" }, func(i *domain.UpsertInput) { i.Content = strings.Repeat("a", 64001) }, func(i *domain.UpsertInput) { i.Evidence = "approved_decision" }, func(i *domain.UpsertInput) { i.Evidence = "verified" }, func(i *domain.UpsertInput) { i.Tags = []string{""} }, func(i *domain.UpsertInput) { i.ExpectedVersion = 1 }, func(i *domain.UpsertInput) { i.Content = string([]byte{0xff}) }} {
		in := input()
		change(&in)
		if _, err := svc.Upsert(ctx, in); code(err) != "validation" {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
}
func TestImportRejectsCycleAndSecretsAtomically(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	if _, err := svc.Upsert(ctx, input()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Memories[0].Status = "superseded"
	snapshot.Memories[0].SupersededBy = snapshot.Memories[0].ID
	if err = svc.Import(ctx, snapshot); code(err) != "validation" {
		t.Fatalf("cycle accepted: %v", err)
	}
	snapshot.Memories[0].Content = "password=hunter22"
	if err = svc.Import(ctx, snapshot); code(err) != "sensitive_content" {
		t.Fatalf("secret accepted: %v", err)
	}
}

func TestRejectSecretsInsideJSONContent(t *testing.T) {
	svc, _ := setup(t)
	in := input()
	in.Content = `{"password":"hunter22"}`
	if _, err := svc.Upsert(context.Background(), in); code(err) != "sensitive_content" {
		t.Fatalf("JSON credential accepted: %v", err)
	}
}
func TestImportRejectsNoncanonicalFilters(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	if _, err := svc.Upsert(ctx, input()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.Memory){func(m *domain.Memory) { m.Tags = []string{"Database"} }, func(m *domain.Memory) {
		m.ExpiresAt = time.Now().Add(-time.Hour).In(time.FixedZone("offset", 14*3600)).Format(time.RFC3339Nano)
	}} {
		data := snapshot
		data.Memories = append([]domain.Memory(nil), snapshot.Memories...)
		change(&data.Memories[0])
		if err = svc.Import(ctx, data); code(err) != "validation" {
			t.Fatalf("noncanonical import accepted by validation: %v", err)
		}
	}
}

func TestRankingAndHandoffPagination(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	in := input()
	in.Title = "Recovery validation"
	in.Content = "sqlite recovery validation"
	title, err := svc.Upsert(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Title = "Other information"
	in.Content = "recovery"
	if _, err = svc.Upsert(ctx, in); err != nil {
		t.Fatal(err)
	}
	hits, err := svc.Search(ctx, domain.SearchInput{Query: "recovery", ProjectKey: "alpha"})
	if err != nil || len(hits.Results) != 2 || hits.Results[0].ID != title.ID {
		t.Fatalf("ranking %+v %v", hits, err)
	}
	h := domain.HandoffInput{Operation: "create", ProjectKey: "alpha", SourceAgent: "codex", Handoff: domain.Handoff{TaskIdentifier: "pagination", Status: "blocked", Summary: "Unicode pagination"}}
	for i := 0; i < 20; i++ {
		h.CompletedWork = append(h.CompletedWork, strings.Repeat("界", 400))
	}
	write, err := svc.Handoff(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	offset := 0
	version := int64(0)
	for {
		r, err := svc.Get(ctx, domain.GetInput{ID: write.ID, MaxResponseChars: 2000, Offset: offset, Version: version})
		if err != nil {
			t.Fatal(err)
		}
		if r.Representation != "handoff_json" || r.Memory.Handoff != nil {
			t.Fatal("partial handoff must be explicit JSON")
		}
		body.WriteString(r.Memory.Content)
		if !r.Truncated {
			break
		}
		offset = *r.NextOffset
		version = r.Memory.Version
	}
	var recovered domain.Handoff
	if err = json.Unmarshal([]byte(body.String()), &recovered); err != nil || len(recovered.CompletedWork) != 20 {
		t.Fatalf("handoff continuation lost data: %v", err)
	}
}

func TestImportValidatesStructuredHandoffs(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	h := domain.HandoffInput{Operation: "create", ProjectKey: "alpha", SourceAgent: "codex", Handoff: domain.Handoff{TaskIdentifier: "import", Status: "blocked", Summary: "valid"}}
	if _, err := svc.Handoff(ctx, h); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Memories[0].Handoff.Summary = ""
	b, _ := json.Marshal(snapshot.Memories[0].Handoff)
	snapshot.Memories[0].Content = string(b)
	if err = svc.Import(ctx, snapshot); code(err) != "validation" {
		t.Fatalf("malformed handoff passed validation: %v", err)
	}
}
