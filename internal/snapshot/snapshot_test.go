package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

const hostile = `</script><script>alert(1)</script><img src=x onerror=alert(1)> {{DATA}} {{SCRIPT}} <!-- ` + " "

func setup(t *testing.T) (*memory.Service, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "db", "memory.db")
	store, err := sqlite.Open(db, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := memory.New(store, config.Config{SearchLimit: 5, MaxResponseChars: 8000})
	ctx := context.Background()
	for _, key := range []string{"alpha", "beta"} {
		if err = svc.RegisterProject(ctx, domain.Project{Key: key, Description: hostile}); err != nil {
			t.Fatal(err)
		}
	}
	return svc, db
}

func seed(t *testing.T, svc *memory.Service) {
	t.Helper()
	ctx := context.Background()
	write := func(in domain.UpsertInput) domain.WriteResult {
		t.Helper()
		in.SourceAgent = "codex"
		r, err := svc.Upsert(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	write(domain.UpsertInput{Scope: "project", ProjectKey: "alpha", Type: "fact", Title: "alpha " + hostile, Content: "body " + hostile, Tags: []string{"tag"}})
	write(domain.UpsertInput{Scope: "project", ProjectKey: "alpha", Type: "fact", Title: "alpha expired", Content: "gone", ExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)})
	write(domain.UpsertInput{Scope: "project", ProjectKey: "beta", Type: "lesson", Title: "beta lesson", Content: "beta body"})
	write(domain.UpsertInput{Scope: "global", Type: "preference", Title: "global pref", Content: "global body"})
	if _, err := svc.Handoff(ctx, domain.HandoffInput{Operation: "create", ProjectKey: "alpha", SourceAgent: "codex", Handoff: domain.Handoff{TaskIdentifier: "t1", Status: "blocked", Summary: "blocked " + hostile, NextActions: []string{"next " + hostile}}}); err != nil {
		t.Fatal(err)
	}
}

func payload(t *testing.T, page []byte) Data {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" id="data">(.*?)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("no data block")
	}
	var data Data
	if err := json.Unmarshal(m[1], &data); err != nil {
		t.Fatalf("data block is not valid JSON: %v", err)
	}
	return data
}

func TestHostileContentStaysData(t *testing.T) {
	svc, _ := setup(t)
	seed(t, svc)
	data, err := Collect(context.Background(), svc, Options{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	page, err := Render(data)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	if n := strings.Count(text, "</script>"); n != 2 {
		t.Fatalf("stored text closed or added script elements: %d closing tags", n)
	}
	block := regexp.MustCompile(`(?s)<script type="application/json" id="data">(.*?)</script>`)
	if strings.ContainsAny(block.FindStringSubmatch(text)[1], "<>&") {
		t.Fatal("data block holds an unescaped markup character")
	}
	for _, bad := range []string{"<img", "<script>alert", "onerror"} {
		if strings.Contains(block.ReplaceAllString(text, ""), bad) {
			t.Fatalf("stored text %q reached the page outside the data block", bad)
		}
	}
	if strings.Count(text, `"use strict"`) != 1 {
		t.Fatal("a placeholder in stored text was substituted")
	}
	round := payload(t, page)
	found := false
	for _, p := range round.Projects {
		for _, m := range p.Memories {
			if m.Title == "alpha "+hostile && m.Content == "body "+hostile {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("hostile text did not round-trip unchanged as data")
	}
}

func TestCSPHashesMatchInlineBlocks(t *testing.T) {
	page, err := Render(Data{Projects: []ProjectData{}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	for _, tag := range []string{"style", "script"} {
		re := regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`)
		body := re.FindStringSubmatch(text)
		if body == nil {
			t.Fatalf("no inline %s", tag)
		}
		sum := sha256.Sum256([]byte(body[1]))
		want := tag + "-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(text, want) {
			t.Fatalf("CSP does not allow the inline %s by hash", tag)
		}
	}
	if !strings.Contains(text, "default-src 'none'") || strings.Contains(text, "unsafe-inline") {
		t.Fatal("CSP must deny everything by default and never allow unsafe-inline")
	}
	for _, network := range []string{"http://", "https://", "fetch(", "XMLHttpRequest", "innerHTML", "eval("} {
		if strings.Contains(text, network) {
			t.Fatalf("page contains %q", network)
		}
	}
}

func TestCollectScopesAndStatuses(t *testing.T) {
	svc, _ := setup(t)
	seed(t, svc)
	ctx := context.Background()
	all, err := Collect(ctx, svc, Options{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, p := range all.Projects {
		keys = append(keys, p.Key)
	}
	if strings.Join(keys, ",") != "alpha,beta,"+GlobalKey {
		t.Fatalf("projects: %v", keys)
	}
	if got := len(all.Projects[0].Memories); got != 2 {
		t.Fatalf("alpha must hold its active fact and handoff, not the expired record: %d", got)
	}
	if all.Projects[0].Summary.ByStatus["expired"] != 1 {
		t.Fatalf("expired count must still be reported: %+v", all.Projects[0].Summary)
	}
	inactive, err := Collect(ctx, svc, Options{ProjectKey: "alpha", IncludeInactive: true}, time.Now())
	if err != nil || len(inactive.Projects) != 1 || len(inactive.Projects[0].Memories) != 3 {
		t.Fatalf("include-inactive must add the expired record and nothing from other projects: %+v %v", inactive, err)
	}
	for _, m := range inactive.Projects[0].Memories {
		if m.ProjectKey != "alpha" {
			t.Fatalf("leaked %q", m.ProjectKey)
		}
	}
	if _, err = Collect(ctx, svc, Options{ProjectKey: "missing"}, time.Now()); err == nil {
		t.Fatal("unregistered project accepted")
	}
}

func TestWritePublishesPrivateNewFile(t *testing.T) {
	svc, db := setup(t)
	seed(t, svc)
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	res, err := Write(ctx, svc, db, "", Options{ProjectKey: "alpha"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Path, filepath.Join(filepath.Dir(db), "views")+string(filepath.Separator)) || !strings.HasSuffix(res.Path, "-alpha.html") {
		t.Fatalf("unexpected default path %s", res.Path)
	}
	info, err := os.Stat(res.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot must be 0600: %v %v", info, err)
	}
	dir, err := os.Stat(filepath.Dir(res.Path))
	if err != nil || dir.Mode().Perm() != 0700 {
		t.Fatalf("views dir must be 0700: %v %v", dir, err)
	}
	if res.Projects != 1 || res.Memories != 2 {
		t.Fatalf("result counts: %+v", res)
	}
	if _, err = Write(ctx, svc, db, res.Path, Options{ProjectKey: "alpha"}, now); err == nil {
		t.Fatal("existing snapshot was overwritten")
	}
	entries, _ := os.ReadDir(filepath.Dir(res.Path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %d entries", len(entries))
	}
}

func TestRecordCapOmitsAndFlags(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.Upsert(ctx, domain.UpsertInput{Scope: "project", ProjectKey: "alpha", Type: "fact", Title: "t", Content: "c" + string(rune('a'+i)), SourceAgent: "codex"}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := Collect(ctx, svc, Options{ProjectKey: "alpha", MaxRecords: 2}, time.Now())
	if err != nil || !data.Truncated || data.OmittedTotal != 1 || data.Projects[0].Omitted != 1 || len(data.Projects[0].Memories) != 2 {
		t.Fatalf("cap must keep 2, omit 1 and flag it: %+v %v", data, err)
	}
	if data.Projects[0].Summary.Total != 3 {
		t.Fatalf("summary must still count omitted records: %+v", data.Projects[0].Summary)
	}
}
