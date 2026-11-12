package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

func TestLegacyProjectPathComparisonDoesNotRewriteIdentity(t *testing.T) {
	s := temporaryStore(t)
	ctx := context.Background()
	p := domain.Project{Key: "legacy", LocalPath: "/work/repo/.", CreatedAt: "2026-11-12T00:00:00.000000000Z"}
	original, _ := json.Marshal(p)
	if _, err := s.write.ExecContext(ctx, "INSERT INTO projects VALUES(?,?)", p.Key, original); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterProject(ctx, domain.Project{Key: p.Key, LocalPath: "/work/repo"}); err != nil {
		t.Fatalf("equivalent existing identity rejected: %v", err)
	}
	if err := s.RegisterProject(ctx, domain.Project{Key: "duplicate", LocalPath: "/work/repo/"}); domain.SafeError(err).Code != "conflict" {
		t.Fatalf("legacy path ownership bypassed: %v", err)
	}
	imported := p
	imported.LocalPath = "/work/repo"
	if err := s.Import(ctx, domain.Export{Projects: []domain.Project{imported}}); err != nil {
		t.Fatalf("equivalent imported identity rejected: %v", err)
	}
	var stored []byte
	if err := s.read.QueryRowContext(ctx, "SELECT document FROM projects WHERE key=?", p.Key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(original) {
		t.Fatal("existing project metadata was silently rewritten")
	}
}
