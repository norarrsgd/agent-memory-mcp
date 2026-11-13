// Package snapshot renders a read-only, self-contained HTML copy of stored memories.
// The page makes no network requests, runs only its own hashed script, and shows stored text through textContent.
package snapshot

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/memory"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

//go:embed web/index.html web/app.js web/style.css
var assets embed.FS

// MaxRecords bounds how many memories one snapshot embeds.
const MaxRecords = 2000

// GlobalKey is the synthetic project key of global records; it cannot collide with a registered project key.
const GlobalKey = "~global"

type Options struct {
	// ProjectKey limits the snapshot to one registered project. Empty means every project plus global records.
	ProjectKey      string
	IncludeInactive bool
	// MaxRecords overrides the default MaxRecords cap; zero keeps the default.
	MaxRecords int
}

type Data struct {
	GeneratedAt     string        `json:"generated_at"`
	IncludeInactive bool          `json:"include_inactive"`
	Truncated       bool          `json:"truncated,omitempty"`
	OmittedTotal    int           `json:"omitted_total,omitempty"`
	Projects        []ProjectData `json:"projects"`
}

type ProjectData struct {
	Key      string         `json:"key"`
	Global   bool           `json:"global,omitempty"`
	Project  domain.Project `json:"project"`
	Summary  domain.Summary `json:"summary"`
	Memories []MemoryData   `json:"memories"`
	Omitted  int            `json:"omitted,omitempty"`
}

// MemoryData is one stored memory. Content is cut to the service response budget; ContentTruncated says so.
type MemoryData struct {
	domain.Memory
	Bucket           string `json:"bucket"`
	HandoffStatus    string `json:"handoff_status,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
	TotalChars       int    `json:"total_chars"`
}

// Collect reads the requested records through the service. It never writes.
func Collect(ctx context.Context, svc *memory.Service, opts Options, now time.Time) (Data, error) {
	data := Data{GeneratedAt: now.UTC().Format(time.RFC3339), IncludeInactive: opts.IncludeInactive, Projects: []ProjectData{}}
	remaining := MaxRecords
	if opts.MaxRecords > 0 {
		remaining = opts.MaxRecords
	}
	add := func(p ProjectData, scope string) error {
		var err error
		in := domain.SummaryInput{Scope: scope}
		if scope == "project" {
			in.ProjectKey = p.Project.Key
		}
		if p.Summary, err = svc.Summary(ctx, in); err != nil {
			return err
		}
		statuses := []string{domain.StatusActive}
		if opts.IncludeInactive {
			statuses = append(statuses, domain.StatusSuperseded, domain.StatusArchived, domain.StatusExpired)
		}
		p.Memories = []MemoryData{}
		for _, status := range statuses {
			after := ""
			for {
				page, err := svc.List(ctx, domain.ListInput{Scope: scope, ProjectKey: in.ProjectKey, Status: status, After: after, Limit: domain.MaxBrowseLimit})
				if err != nil {
					return err
				}
				for _, ref := range page.Items {
					if remaining == 0 {
						p.Omitted++
						continue
					}
					got, err := svc.Get(ctx, domain.GetInput{ID: ref.ID})
					if err != nil {
						return err
					}
					remaining--
					p.Memories = append(p.Memories, MemoryData{Memory: got.Memory, Bucket: ref.Status, HandoffStatus: ref.HandoffStatus, ContentTruncated: got.Truncated, TotalChars: got.TotalChars})
				}
				if page.NextCursor == "" {
					break
				}
				after = page.NextCursor
			}
		}
		data.OmittedTotal += p.Omitted
		data.Projects = append(data.Projects, p)
		return nil
	}
	if opts.ProjectKey != "" {
		project, err := svc.Project(ctx, opts.ProjectKey)
		if err != nil {
			return data, err
		}
		err = add(ProjectData{Key: project.Key, Project: project}, "project")
		data.Truncated = data.OmittedTotal > 0
		return data, err
	}
	after := ""
	for {
		page, err := svc.Projects(ctx, domain.ProjectsInput{After: after})
		if err != nil {
			return data, err
		}
		for _, project := range page.Projects {
			if err = add(ProjectData{Key: project.Key, Project: project}, "project"); err != nil {
				return data, err
			}
		}
		if page.NextCursor == "" {
			break
		}
		after = page.NextCursor
	}
	err := add(ProjectData{Key: GlobalKey, Global: true, Project: domain.Project{Key: GlobalKey}}, "global")
	data.Truncated = data.OmittedTotal > 0
	return data, err
}

// Render returns the complete HTML page for data.
func Render(data Data) ([]byte, error) {
	// encoding/json escapes <, > and & so the payload cannot close its script element.
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	read := func(name string) string {
		b, _ := assets.ReadFile("web/" + name)
		return string(b)
	}
	style, script := read("style.css"), read("app.js")
	csp := "default-src 'none'; script-src 'sha256-" + hash(script) + "'; style-src 'sha256-" + hash(style) + "'; base-uri 'none'; form-action 'none'"
	page := strings.NewReplacer("{{CSP}}", csp, "{{STYLE}}", style, "{{SCRIPT}}", script).Replace(read("index.html"))
	// Insert the payload last so stored text containing a placeholder is never substituted.
	return []byte(strings.Replace(page, "{{DATA}}", string(payload), 1)), nil
}

func hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// DefaultPath names a new snapshot inside the views directory next to the database.
func DefaultPath(dbPath, project string, now time.Time) string {
	name := now.UTC().Format("20060102T150405.000000000Z")
	if project != "" {
		name += "-" + project
	}
	return filepath.Join(filepath.Dir(dbPath), "views", name+".html")
}

// MaxBytes bounds the size of one rendered page.
const MaxBytes = 64 << 20

type Result struct {
	Path        string `json:"path"`
	Projects    int    `json:"projects"`
	Memories    int    `json:"memories"`
	Truncated   bool   `json:"truncated,omitempty"`
	GeneratedAt string `json:"generated_at"`
}

// Write collects, renders and publishes one snapshot at path, which must be new. An empty path selects DefaultPath.
func Write(ctx context.Context, svc *memory.Service, dbPath, path string, opts Options, now time.Time) (Result, error) {
	if path == "" {
		path = DefaultPath(dbPath, opts.ProjectKey, now)
	}
	data, err := Collect(ctx, svc, opts, now)
	if err != nil {
		return Result{}, err
	}
	page, err := Render(data)
	if err != nil {
		return Result{}, err
	}
	if len(page) > MaxBytes {
		return Result{}, domain.Err("limit", "snapshot exceeds its size limit; narrow it to one project")
	}
	err = sqlite.WritePrivateFile(path, func(w io.Writer) error {
		_, err := w.Write(page)
		return err
	})
	out := Result{Path: path, Projects: len(data.Projects), Truncated: data.Truncated, GeneratedAt: data.GeneratedAt}
	for _, p := range data.Projects {
		out.Memories += len(p.Memories)
	}
	return out, err
}
