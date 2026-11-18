package memory

import (
	"context"
	"regexp"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

const defaultBrowseLimit = 20

// browseCursor is "<updated_at>~<id>": the normalized timestamp and UUID of the last item on the previous page.
var browseCursor = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z)~([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$`)

func (s *Service) browseScope(ctx context.Context, scope, key string) error {
	switch scope {
	case "global":
		if key != "" {
			return domain.Err("validation", "global scope does not take a project_key")
		}
		return nil
	case "project":
		return s.project(ctx, key)
	}
	return domain.Err("validation", "scope must be global or project")
}

// Summary returns record counts for one project or for global records. It is read-only.
func (s *Service) Summary(ctx context.Context, in domain.SummaryInput) (domain.Summary, error) {
	if err := s.browseScope(ctx, in.Scope, in.ProjectKey); err != nil {
		return domain.Summary{}, err
	}
	return s.store.Summary(ctx, in.Scope, in.ProjectKey)
}

// List returns content-free references to one scope's memories, newest first. Use Get for a memory's content.
func (s *Service) List(ctx context.Context, in domain.ListInput) (domain.ListResult, error) {
	out := domain.ListResult{Items: []domain.MemoryRef{}}
	if err := s.browseScope(ctx, in.Scope, in.ProjectKey); err != nil {
		return out, err
	}
	if in.Status == "" {
		in.Status = domain.StatusActive
	}
	switch in.Status {
	case domain.StatusActive, domain.StatusSuperseded, domain.StatusArchived, domain.StatusExpired:
	default:
		return out, domain.Err("validation", "status must be active, superseded, archived or expired")
	}
	if in.Type != "" && !domain.ValidMemoryType(in.Type) {
		return out, domain.Err("validation", "type must be one of: "+domain.MemoryTypeList())
	}
	switch in.HandoffStatus {
	case "":
	case "in_progress", "blocked", "completed":
		if in.Type != "" && in.Type != "handoff" {
			return out, domain.Err("validation", "handoff_status requires type handoff")
		}
		in.Type = "handoff"
	default:
		return out, domain.Err("validation", "handoff_status must be in_progress, blocked or completed")
	}
	if in.Limit == 0 {
		in.Limit = defaultBrowseLimit
	}
	if in.Limit < 1 || in.Limit > domain.MaxBrowseLimit {
		return out, domain.Err("validation", "limit must be 1..50; omit or use 0 for default 20")
	}
	var updated, id string
	if in.After != "" {
		m := browseCursor.FindStringSubmatch(in.After)
		if m == nil {
			return out, domain.Err("validation", "after must be the next_cursor from a previous list; omit it for the first page")
		}
		updated, id = m[1], m[2]
	}
	page := in.Limit
	in.Limit++
	refs, err := s.store.List(ctx, in, updated, id)
	if err != nil {
		return out, err
	}
	if len(refs) > page {
		refs = refs[:page]
		last := refs[page-1]
		out.NextCursor = last.UpdatedAt + "~" + last.ID
	}
	out.Items = refs
	return out, nil
}

// Project returns one registered project's metadata and active memory count.
func (s *Service) Project(ctx context.Context, key string) (domain.Project, error) {
	if err := s.project(ctx, key); err != nil {
		return domain.Project{}, err
	}
	return s.store.Project(ctx, key)
}
