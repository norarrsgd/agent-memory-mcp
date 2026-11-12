package memory

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

// Credential-shaped values are assembled at run time so the test source holds no scannable secret.
func TestSecretPatterns(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a1", n)[:n] }
	rejected := map[string]string{
		"cosmos account key":     "AccountEndpoint=https://x.documents.azure.com:443/;AccountKey=" + a(40) + "==;",
		"service bus key":        "Endpoint=sb://x.servicebus.windows.net/;SharedAccessKeyName=root;SharedAccessKey=" + a(32),
		"sas signature":          "https://acct.blob.core.windows.net/c/b?sv=2022-11-02&sig=" + a(30) + "%3D",
		"shared access sig":      "SharedAccessSignature=sv=2022-11-02&ss=b",
		"bearer token":           "send Bearer " + a(30),
		"token assignment":       "token = " + a(24),
		"secret assignment":      "secret: " + a(20),
		"private key assignment": "private_key = " + a(24),
		"google api key":         "AIza" + a(35),
		"stripe live key":        "sk_live_" + a(24),
		"npm token":              "npm_" + a(36),
		"pypi token":             "pypi-" + a(30),
		"token-only url":         "https://" + a(40) + "@github.com/o/r.git",
		"env password":           "DB_PASSWORD=hunter2hunter2",
		"env token":              "GITHUB_TOKEN=" + a(26),
		"env api key":            "OPENAI_API_KEY=abcd1234",
		"aws secret access key":  "AWS_SECRET_ACCESS_KEY=" + a(40),
	}
	for name, text := range rejected {
		if err := sensitive(text); code(err) != "sensitive_content" {
			t.Errorf("%s accepted: %q", name, text)
		}
	}
	accepted := []string{
		"The token budget is tracked per request",
		"token: 42",
		"secret sauce recipe",
		"tokenizer: unicode61",
		"Use bearer authentication for the API",
		"AccountKey is stored in Key Vault",
		"sig=short",
		"credentials: none",
		"SharedAccessKeyName=root",
		"Secret: key-vault-secret-name-prod",
		"token: some-long-identifier-name-here",
		"credentials: see docs.internal.example",
		"secret: AZURE_KEY_VAULT_SECRET_NAME",
		"ssh://git@github.com/owner/repo.git",
		"https://org@dev.azure.com/org/proj/_git/repo",
		"SQLite migration needs transactional preflight checks",
	}
	for _, text := range accepted {
		if err := sensitive(text); err != nil {
			t.Errorf("false positive: %q: %v", text, err)
		}
	}
}

func TestRemoteURLCredentials(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	for _, remote := range []string{"https://user:pw@example.com/o/r.git", "https://user:@example.com/o/r.git"} {
		if err := svc.RegisterProject(ctx, domain.Project{Key: "bad-remote", RemoteURL: remote}); code(err) != "sensitive_content" {
			t.Errorf("registration accepted %q: %v", remote, err)
		}
	}
	for _, remote := range []string{"ssh://git@github.com/o/r.git", "https://org@dev.azure.com/org/proj/_git/repo", "git@github.com:o/r.git"} {
		if err := svc.RegisterProject(ctx, domain.Project{Key: "ok-" + strings.NewReplacer("/", "-", ":", "-", "@", "-").Replace(remote)[:12], RemoteURL: remote}); err != nil {
			t.Errorf("registration rejected %q: %v", remote, err)
		}
	}
	for _, remote := range []string{"https://user:pa ss@example.com/o/r.git", "https://user:pa\tss@example.com/o/r.git"} {
		if err := svc.RegisterProject(ctx, domain.Project{Key: "unparsable", RemoteURL: remote}); code(err) != "sensitive_content" {
			t.Errorf("unparsable credentialed remote accepted %q: %v", remote, err)
		}
	}
	// Import must apply the same URL rule as registration, not only the generic scan.
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Projects = []domain.Project{{Key: "import-bad", RemoteURL: "https://user:@example.com/o/r.git", CreatedAt: "2026-11-12T00:00:00.000000000Z"}}
	snapshot.Memories, snapshot.Receipts = []domain.Memory{}, []domain.Receipt{}
	if err = svc.Import(ctx, snapshot); code(err) != "sensitive_content" {
		t.Errorf("import accepted an empty-password remote: %v", err)
	}
}

// The shipped example arguments and docs must stay storable under the scanner.
func TestExamplesAreNotFlagged(t *testing.T) {
	for _, path := range []string{"../../examples/memories.json", "../../examples/claude/CLAUDE.md", "../../examples/codex/AGENTS.md"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = sensitive(string(b)); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestAuditFlagsStoredRecordsThatPredateAPattern(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	clean, err := svc.Upsert(ctx, input())
	if err != nil {
		t.Fatal(err)
	}
	// Written through the store, as an older release would have stored text the current patterns reject.
	old := domain.Memory{ID: "00000000-0000-4000-8000-000000000042", Scope: "global", Type: "fact", Title: "Old", Content: "token = " + strings.Repeat("a1", 12), Tags: []string{}, SourceAgent: "test", Status: "active", Evidence: "observation"}
	if _, err = s.Put(ctx, old, 0, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterProject(ctx, domain.Project{Key: "old-remote", RemoteURL: "https://user:@example.com/o/r.git"}); err != nil {
		t.Fatal(err)
	}
	// A record larger than the 256 KiB input limit once escaped must be scanned, not reported as flagged.
	big := domain.Memory{ID: "00000000-0000-4000-8000-000000000043", Scope: "global", Type: "fact", Title: "Big", Content: strings.Repeat("\x01", 60000), Tags: []string{}, SourceAgent: "test", Status: "active", Evidence: "observation"}
	if _, err = s.Put(ctx, big, 0, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Audit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FlaggedMemories) != 1 || result.FlaggedMemories[0] != old.ID {
		t.Fatalf("flagged memories %v, want only %s (clean %s, big %s)", result.FlaggedMemories, old.ID, clean.ID, big.ID)
	}
	if len(result.FlaggedProjects) != 1 || result.FlaggedProjects[0] != "old-remote" {
		t.Fatalf("flagged projects %v", result.FlaggedProjects)
	}
	if result.Checked < 5 {
		t.Fatalf("checked %d records", result.Checked)
	}
}

func TestImportRejectionNamesTheRecord(t *testing.T) {
	svc, s := setup(t)
	ctx := context.Background()
	stored, err := svc.Upsert(ctx, input())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Memories[0].Content = "GITHUB_TOKEN=" + strings.Repeat("a1", 13)
	err = svc.Import(ctx, snapshot)
	if code(err) != "sensitive_content" || !strings.Contains(err.Error(), "memory "+stored.ID) || strings.Contains(err.Error(), "a1a1") {
		t.Fatalf("import error must name the record and never echo the value: %v", err)
	}
}
