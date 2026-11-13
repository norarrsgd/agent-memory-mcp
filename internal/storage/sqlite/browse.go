package sqlite

import (
	"context"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

// statusBucket maps stored status plus expiry onto the browse statuses; its single placeholder is the current time.
const statusBucket = `CASE WHEN status='active' AND expires_at IS NOT NULL AND expires_at<=? THEN 'expired' ELSE status END`

func scopeClause(scope, project string) (string, []any) {
	if scope == "global" {
		return "scope='global'", nil
	}
	return "project_key=?", []any{project}
}

// Summary counts a scope's records by status, type and handoff status without reading document bodies.
func (s *Store) Summary(ctx context.Context, scope, project string) (domain.Summary, error) {
	out := domain.Summary{
		ByType:          map[string]int{},
		ByStatus:        map[string]int{domain.StatusActive: 0, domain.StatusSuperseded: 0, domain.StatusArchived: 0, domain.StatusExpired: 0},
		ByHandoffStatus: map[string]int{},
	}
	where, args := scopeClause(scope, project)
	rows, err := s.read.QueryContext(ctx, "SELECT "+statusBucket+" AS bucket,type,coalesce(handoff_status,''),count(*) FROM memories WHERE "+where+" GROUP BY 1,2,3", append([]any{now()}, args...)...)
	if err != nil {
		return out, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket, kind, handoff string
		var n int
		if err = rows.Scan(&bucket, &kind, &handoff, &n); err != nil {
			return out, mapError(err)
		}
		out.ByStatus[bucket] += n
		if bucket != domain.StatusActive {
			continue
		}
		out.Total += n
		out.ByType[kind] += n
		if kind == "handoff" && handoff != "" {
			out.ByHandoffStatus[handoff] += n
		}
	}
	return out, mapError(rows.Err())
}

// List returns one page ordered by newest update then ID; cursorUpdated and cursorID select the page after a previous item.
func (s *Store) List(ctx context.Context, in domain.ListInput, cursorUpdated, cursorID string) ([]domain.MemoryRef, error) {
	where, args := scopeClause(in.Scope, in.ProjectKey)
	query := "SELECT id,title,type," + statusBucket + ",version,updated_at,coalesce(json_extract(document,'$.evidence'),''),coalesce(handoff_status,'') FROM memories WHERE " + where
	args = append([]any{now()}, args...)
	switch in.Status {
	case domain.StatusExpired:
		query += " AND status='active' AND expires_at IS NOT NULL AND expires_at<=?"
		args = append(args, now())
	case domain.StatusActive:
		query += " AND status='active' AND (expires_at IS NULL OR expires_at>?)"
		args = append(args, now())
	default:
		query += " AND status=?"
		args = append(args, in.Status)
	}
	if in.Type != "" {
		query += " AND type=?"
		args = append(args, in.Type)
	}
	if in.HandoffStatus != "" {
		query += " AND handoff_status=?"
		args = append(args, in.HandoffStatus)
	}
	if cursorID != "" {
		query += " AND (updated_at<? OR (updated_at=? AND id>?))"
		args = append(args, cursorUpdated, cursorUpdated, cursorID)
	}
	query += " ORDER BY updated_at DESC,id ASC LIMIT ?"
	args = append(args, in.Limit)
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	refs := []domain.MemoryRef{}
	for rows.Next() {
		var r domain.MemoryRef
		if err = rows.Scan(&r.ID, &r.Title, &r.Type, &r.Status, &r.Version, &r.UpdatedAt, &r.Evidence, &r.HandoffStatus); err != nil {
			return nil, mapError(err)
		}
		if r.Type != "handoff" {
			r.HandoffStatus = ""
		}
		refs = append(refs, r)
	}
	return refs, mapError(rows.Err())
}
