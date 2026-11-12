package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

// named adds the record to a credential rejection so a failed import says which record to fix.
func named(err error, kind, id string) error {
	if err != nil && domain.SafeError(err).Code == "sensitive_content" {
		return domain.Err("sensitive_content", domain.SafeError(err).Message+" ("+kind+" "+id+")")
	}
	return err
}

func (s *Service) Import(ctx context.Context, data domain.Export) error {
	if data.FormatVersion != 1 {
		return domain.Err("validation", "unsupported export format")
	}
	if len(data.Memories) > 100000 || len(data.Projects) > domain.MaxProjects || len(data.Receipts) > 200000 {
		return domain.Err("validation", "import contains too many records")
	}
	if _, err := time.Parse(time.RFC3339Nano, data.ExportedAt); err != nil {
		return domain.Err("validation", "invalid export timestamp")
	}
	projects := map[string]bool{}
	memories := map[string]domain.Memory{}
	receipts := map[string]bool{}
	for _, p := range data.Projects {
		if projects[p.Key] || !keyRE.MatchString(p.Key) || p.MemoryCount != 0 {
			return domain.Err("validation", "invalid or duplicate imported project")
		}
		projects[p.Key] = true
		if p.LocalPath != "" && !filepath.IsAbs(p.LocalPath) {
			return domain.Err("validation", "local_path must be absolute")
		}
		if err := scan(p); err != nil {
			return named(err, "project", p.Key)
		}
		if err := checkRemoteURL(p.RemoteURL); err != nil {
			return named(err, "project", p.Key)
		}
		if _, err := time.Parse(time.RFC3339Nano, p.CreatedAt); err != nil {
			return domain.Err("validation", "invalid project timestamp")
		}
		for _, v := range []struct {
			value string
			max   int
		}{{p.RemoteURL, 512}, {p.LocalPath, 1024}, {p.Organization, 160}, {p.Description, 1000}} {
			if err := field("project metadata", v.value, v.max, false); err != nil {
				return err
			}
		}
	}
	for _, m := range data.Memories {
		if _, exists := memories[m.ID]; exists || !idRE.MatchString(m.ID) || m.Version < 1 {
			return domain.Err("validation", "invalid or duplicate imported memory")
		}
		if err := scan(m); err != nil {
			return named(err, "memory", m.ID)
		}
		if m.ProjectKey != "" && !projects[m.ProjectKey] {
			if err := s.project(ctx, m.ProjectKey); err != nil {
				return err
			}
		}
		in := domain.UpsertInput{ID: m.ID, ExpectedVersion: m.Version, Scope: m.Scope, ProjectKey: m.ProjectKey, Type: m.Type, Title: m.Title, Content: m.Content, Tags: m.Tags, SourceAgent: m.SourceAgent, SourceReference: m.SourceReference, Evidence: m.Evidence, LastVerifiedAt: m.LastVerifiedAt, ExpiresAt: m.ExpiresAt, IdempotencyKey: m.IdempotencyKey}
		if err := s.validate(ctx, &in, m.Handoff != nil, false); err != nil {
			return named(err, "memory", m.ID)
		}
		originalTags, _ := json.Marshal(m.Tags)
		normalizedTags, _ := json.Marshal(in.Tags)
		if string(originalTags) != string(normalizedTags) || in.Evidence != m.Evidence || in.ExpiresAt != m.ExpiresAt || in.LastVerifiedAt != m.LastVerifiedAt {
			return domain.Err("validation", "import fields must use canonical tags, evidence and UTC timestamps")
		}
		for _, ts := range []string{m.CreatedAt, m.UpdatedAt} {
			normalized, err := timestamp(ts)
			if err != nil || normalized != ts || ts == "" {
				return domain.Err("validation", "import timestamps must be canonical UTC")
			}
		}
		if m.CreatedAt > m.UpdatedAt {
			return domain.Err("validation", "invalid timestamp order")
		}
		if m.Status != "active" && m.Status != "superseded" && m.Status != "archived" {
			return domain.Err("validation", "invalid lifecycle status")
		}
		if (m.Status == "superseded") != (m.SupersededBy != "") {
			return domain.Err("validation", "invalid supersession relationship")
		}
		if m.Type == "handoff" {
			if m.Handoff == nil || !keyRE.MatchString(m.Handoff.TaskIdentifier) || (m.Handoff.Status != "blocked" && m.Handoff.Status != "in_progress" && m.Handoff.Status != "completed") || m.Scope != "project" {
				return domain.Err("validation", "invalid imported handoff")
			}
			if err := validateHandoff(*m.Handoff); err != nil {
				return err
			}
			b, _ := json.Marshal(m.Handoff)
			if string(b) != m.Content {
				return domain.Err("validation", "handoff content differs from structured data")
			}
		} else if m.Handoff != nil {
			return domain.Err("validation", "handoff data on ordinary memory")
		}
		if err := field("archive reason", m.ArchiveReason, 512, false); err != nil {
			return err
		}
		memories[m.ID] = m
	}
	lookup := func(id string) (domain.Memory, error) {
		if m, ok := memories[id]; ok {
			return m, nil
		}
		return s.store.Get(ctx, id)
	}
	for _, m := range data.Memories {
		if m.SupersededBy != "" {
			seen := map[string]bool{m.ID: true}
			id := m.SupersededBy
			for id != "" {
				if seen[id] {
					return domain.Err("validation", "supersession cycle")
				}
				seen[id] = true
				other, err := lookup(id)
				if err != nil {
					return err
				}
				if other.Scope != m.Scope || other.ProjectKey != m.ProjectKey {
					return domain.Err("validation", "cross-project supersession")
				}
				id = other.SupersededBy
			}
		}
		if m.Handoff != nil {
			for _, id := range m.Handoff.RelatedMemoryIDs {
				other, err := lookup(id)
				if err != nil {
					return err
				}
				if other.Scope != "global" && other.ProjectKey != m.ProjectKey {
					return domain.Err("validation", "cross-project handoff reference")
				}
			}
		}
	}
	hashRE := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, r := range data.Receipts {
		key := r.ScopeKey + "\x00" + r.Key
		if receipts[key] || !hashRE.MatchString(r.Hash) || r.Version < 1 {
			return domain.Err("validation", "invalid idempotency receipt")
		}
		receipts[key] = true
		if err := scan(r); err != nil {
			return err
		}
		if err := field("idempotency key", r.Key, 128, true); err != nil {
			return err
		}
		m, err := lookup(r.ID)
		if err != nil {
			return err
		}
		if r.ScopeKey != m.Scope+":"+m.ProjectKey || r.Version > m.Version || strings.ContainsRune(r.ScopeKey, 0) {
			return domain.Err("validation", "invalid receipt relationship")
		}
	}
	return s.store.Import(ctx, data)
}
