package domain

import (
	"encoding/json"
	"errors"
)

type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Retryable      bool   `json:"retryable,omitempty"`
	CurrentVersion int64  `json:"current_version,omitempty"`
}

func (e *Error) Error() string       { return e.Code + ": " + e.Message }
func Err(code, message string) error { return &Error{Code: code, Message: message} }
func Conflict(version int64) error {
	return &Error{Code: "conflict", Message: "record changed; retrieve current version", CurrentVersion: version}
}
func SafeError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: "storage_failure", Message: "storage operation failed"}
}

type Project struct {
	Key          string `json:"key"`
	RemoteURL    string `json:"remote_url,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	Organization string `json:"organization,omitempty"`
	Description  string `json:"description,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	MemoryCount  int    `json:"memory_count"`
}

type Memory struct {
	ID              string   `json:"id"`
	Scope           string   `json:"scope"`
	ProjectKey      string   `json:"project_key,omitempty"`
	Type            string   `json:"type"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Tags            []string `json:"tags"`
	SourceAgent     string   `json:"source_agent"`
	SourceReference string   `json:"source_reference,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	LastVerifiedAt  string   `json:"last_verified_at,omitempty"`
	Version         int64    `json:"version"`
	Status          string   `json:"status"`
	Evidence        string   `json:"evidence"`
	SupersededBy    string   `json:"superseded_by,omitempty"`
	ExpiresAt       string   `json:"expires_at,omitempty"`
	IdempotencyKey  string   `json:"idempotency_key,omitempty"`
	ArchiveReason   string   `json:"archive_reason,omitempty"`
	Handoff         *Handoff `json:"handoff,omitempty"`
}

type UpsertInput struct {
	ID                string   `json:"id,omitempty"`
	Scope             string   `json:"scope"`
	ProjectKey        string   `json:"project_key,omitempty"`
	Type              string   `json:"type"`
	Title             string   `json:"title"`
	Content           string   `json:"content"`
	Tags              []string `json:"tags,omitempty"`
	SourceAgent       string   `json:"source_agent"`
	SourceReference   string   `json:"source_reference,omitempty"`
	ExpectedVersion   int64    `json:"expected_version,omitempty"`
	IdempotencyKey    string   `json:"idempotency_key,omitempty"`
	Evidence          string   `json:"evidence,omitempty" jsonschema:"observation (default), hypothesis, verified, or approved_decision. verified requires source_reference and last_verified_at; approved_decision requires type decision and source_reference"`
	LastVerifiedAt    string   `json:"last_verified_at,omitempty" jsonschema:"RFC3339 time the claim was checked, e.g. 2026-11-12T14:30:00Z. Required when evidence is verified; rejected for other evidence except approved_decision; must not be in the future"`
	ExpiresAt         string   `json:"expires_at,omitempty"`
	SupersedesID      string   `json:"supersedes_id,omitempty"`
	SupersedesVersion int64    `json:"supersedes_version,omitempty"`
}

type WriteResult struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type ArchiveInput struct {
	ID              string `json:"id"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason,omitempty"`
}
type SearchInput struct {
	Query            string   `json:"query"`
	Scope            string   `json:"scope,omitempty"`
	ProjectKey       string   `json:"project_key,omitempty"`
	MemoryTypes      []string `json:"memory_types,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	MaxResponseChars int      `json:"max_response_chars,omitempty"`
}
type Hit struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Type      string  `json:"type"`
	Project   string  `json:"project,omitempty"`
	Excerpt   string  `json:"excerpt"`
	Score     float64 `json:"score"`
	UpdatedAt string  `json:"updated_at"`
}
type SearchResult struct {
	Results       []Hit `json:"results"`
	BudgetLimited bool  `json:"budget_limited,omitempty"`
}
type GetInput struct {
	ID               string `json:"id"`
	MaxResponseChars int    `json:"max_response_chars,omitempty"`
	Offset           int    `json:"offset,omitempty"`
	Version          int64  `json:"version,omitempty"`
}
type GetResult struct {
	Memory         Memory `json:"memory"`
	Truncated      bool   `json:"truncated"`
	NextOffset     *int   `json:"next_offset,omitempty"`
	TotalChars     int    `json:"total_chars"`
	Representation string `json:"representation,omitempty"`
}
type Handoff struct {
	TaskIdentifier      string            `json:"task_identifier,omitempty"`
	Status              string            `json:"status,omitempty"`
	Summary             string            `json:"summary,omitempty"`
	CompletedWork       []string          `json:"completed_work,omitempty"`
	Blockers            []string          `json:"blockers,omitempty"`
	NextActions         []string          `json:"next_actions,omitempty"`
	Branch              string            `json:"branch,omitempty"`
	CommitReference     string            `json:"commit_reference,omitempty"`
	CommittedChanges    []string          `json:"committed_changes,omitempty"`
	UncommittedChanges  []string          `json:"uncommitted_changes,omitempty"`
	VerificationResults map[string]string `json:"verification_results,omitempty"`
	RelatedMemoryIDs    []string          `json:"related_memory_ids,omitempty"`
}
type HandoffInput struct {
	Operation       string `json:"operation"`
	ID              string `json:"id,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	ProjectKey      string `json:"project_key"`
	SourceAgent     string `json:"source_agent,omitempty"`
	SourceReference string `json:"source_reference,omitempty"`
	Handoff
}
type ProjectsInput struct {
	Limit int    `json:"limit,omitempty"`
	After string `json:"after,omitempty"`
	// Register switches the call from listing to registering one project.
	Register *Project `json:"register,omitempty"`
}
type ProjectsResult struct {
	Projects   []Project `json:"projects"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type LatestResult struct {
	ID             string `json:"id"`
	Version        int64  `json:"version"`
	UpdatedAt      string `json:"updated_at"`
	TaskIdentifier string `json:"task_identifier,omitempty"`
}
type HandoffResult struct {
	ID             string `json:"id"`
	Version        int64  `json:"version"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	TaskIdentifier string `json:"task_identifier,omitempty"`
}
type Export struct {
	FormatVersion int       `json:"format_version"`
	ExportedAt    string    `json:"exported_at"`
	Projects      []Project `json:"projects"`
	Memories      []Memory  `json:"memories"`
	Receipts      []Receipt `json:"receipts"`
}
type Receipt struct {
	ScopeKey string `json:"scope_key"`
	Key      string `json:"key"`
	Hash     string `json:"hash"`
	ID       string `json:"id"`
	Version  int64  `json:"version"`
}

// WireChars counts both structured data and the JSON text mirror used by MCP clients.
func WireChars(v any) int {
	b, _ := json.Marshal(v)
	mirror, _ := json.Marshal(string(b))
	return len([]rune(string(b))) + len([]rune(string(mirror))) + 100
}
func CheckInputSize(v any) error {
	b, err := json.Marshal(v)
	if err != nil || len(b) > 256*1024 {
		return Err("validation", "input exceeds 256 KiB")
	}
	return nil
}
