package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/norarrsgd/agent-memory-mcp/internal/config"
	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	"github.com/norarrsgd/agent-memory-mcp/internal/storage/sqlite"
)

type Service struct {
	store  *sqlite.Store
	config config.Config
}

func New(s *sqlite.Store, c config.Config) *Service { return &Service{store: s, config: c} }

var keyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)
var idRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var secrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,})`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|api[_-]?key|access[_-]?token|client[_-]?secret|authorization)\s*["']?\s*[:=]\s*["']?[^\s"';,}]{3,}`),
	regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s/@:]+:[^\s/@]+@`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
}

func sensitive(text string) error {
	for _, re := range secrets {
		if re.MatchString(text) {
			return domain.Err("sensitive_content", "content matches a credential pattern; remove secrets before storing")
		}
	}
	return nil
}
func scan(v any) error {
	if err := domain.CheckInputSize(v); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return domain.Err("validation", "invalid input")
	}
	if err := sensitive(string(b)); err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return domain.Err("validation", "invalid input")
	}
	return scanStrings(decoded)
}

// Scan original string values too: JSON escaping can conceal credential assignments.
func scanStrings(v any) error {
	switch value := v.(type) {
	case string:
		return sensitive(value)
	case []any:
		for _, item := range value {
			if err := scanStrings(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range value {
			if err := sensitive(key); err != nil {
				return err
			}
			if err := scanStrings(item); err != nil {
				return err
			}
		}
	}
	return nil
}
func field(name, value string, max int, required bool) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || len([]rune(value)) > max || (required && strings.TrimSpace(value) == "") {
		return domain.Err("validation", name+" is invalid or exceeds its limit")
	}
	return nil
}
func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("OS random source unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func memoryType(v string) bool {
	switch v {
	case "fact", "decision", "lesson", "task", "handoff", "preference":
		return true
	}
	return false
}
func timestamp(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", domain.Err("validation", "timestamp must be RFC3339")
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z"), nil
}
func (s *Service) project(ctx context.Context, key string) error {
	if !keyRE.MatchString(key) {
		return domain.Err("validation", "invalid project key")
	}
	if err := s.store.ProjectExists(ctx, key); err != nil {
		if domain.SafeError(err).Code == "not_found" {
			return domain.Err("validation", "project is not registered")
		}
		return err
	}
	return nil
}
func tags(in []string) ([]string, error) {
	if len(in) > 20 {
		return nil, domain.Err("validation", "at most 20 tags are allowed")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, tag := range in {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if err := field("tag", tag, 64, true); err != nil {
			return nil, err
		}
		if !seen[tag] {
			out = append(out, tag)
			seen[tag] = true
		}
	}
	sort.Strings(out)
	return out, nil
}
func (s *Service) validate(ctx context.Context, in *domain.UpsertInput, allowHandoff bool, checkProject bool) error {
	if err := scan(in); err != nil {
		return err
	}
	if in.ID != "" && !idRE.MatchString(in.ID) {
		return domain.Err("validation", "invalid memory ID")
	}
	if (in.ID != "" && in.ExpectedVersion < 1) || (in.ID == "" && in.ExpectedVersion != 0) {
		return domain.Err("validation", "updates require expected_version; creations must omit it")
	}
	switch in.Scope {
	case "global":
		if in.ProjectKey != "" {
			return domain.Err("validation", "global memory cannot have a project")
		}
	case "project":
		if !keyRE.MatchString(in.ProjectKey) {
			return domain.Err("validation", "invalid project key")
		}
		if checkProject {
			if err := s.project(ctx, in.ProjectKey); err != nil {
				return err
			}
		}
	default:
		return domain.Err("validation", "scope must be global or project")
	}
	if !memoryType(in.Type) || (!allowHandoff && in.Type == "handoff") {
		return domain.Err("validation", "invalid memory type; handoffs use memory_handoff")
	}
	for _, f := range []struct {
		name, value string
		max         int
		required    bool
	}{{"title", in.Title, 160, true}, {"content", in.Content, 64000, true}, {"source_agent", in.SourceAgent, 80, true}, {"source_reference", in.SourceReference, 512, false}, {"idempotency_key", in.IdempotencyKey, 128, false}} {
		if err := field(f.name, f.value, f.max, f.required); err != nil {
			return err
		}
	}
	var err error
	in.Tags, err = tags(in.Tags)
	if err != nil {
		return err
	}
	if in.Evidence == "" {
		in.Evidence = "observation"
	}
	switch in.Evidence {
	case "observation", "hypothesis", "verified", "approved_decision":
	default:
		return domain.Err("validation", "invalid evidence classification")
	}
	if in.Evidence == "approved_decision" && in.Type != "decision" {
		return domain.Err("validation", "approved_decision requires decision type")
	}
	if (in.Evidence == "verified" || in.Evidence == "approved_decision") && strings.TrimSpace(in.SourceReference) == "" {
		return domain.Err("validation", "verification and approval require source_reference")
	}
	in.LastVerifiedAt, err = timestamp(in.LastVerifiedAt)
	if err != nil {
		return err
	}
	in.ExpiresAt, err = timestamp(in.ExpiresAt)
	if err != nil {
		return err
	}
	if in.Evidence == "verified" && in.LastVerifiedAt == "" {
		return domain.Err("validation", "verified evidence requires last_verified_at (RFC3339, e.g. 2026-11-12T14:30:00Z)")
	}
	if in.LastVerifiedAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, in.LastVerifiedAt)
		if t.After(time.Now().Add(time.Minute)) {
			return domain.Err("validation", "verification timestamp is in the future")
		}
		if in.Evidence != "verified" && in.Evidence != "approved_decision" {
			return domain.Err("validation", "verification timestamp requires verified evidence")
		}
	}
	if in.SupersedesID != "" && (!idRE.MatchString(in.SupersedesID) || in.SupersedesVersion < 1 || in.ID != "") {
		return domain.Err("validation", "supersession requires a new record and target version")
	}
	if in.SupersedesID == "" && in.SupersedesVersion != 0 {
		return domain.Err("validation", "supersedes_id is required")
	}
	return nil
}
func (s *Service) upsert(ctx context.Context, in domain.UpsertInput, h *domain.Handoff) (domain.WriteResult, error) {
	if err := s.validate(ctx, &in, h != nil, true); err != nil {
		return domain.WriteResult{}, err
	}
	b, _ := json.Marshal(struct {
		Input   domain.UpsertInput
		Handoff *domain.Handoff
	}{in, h})
	hash := sha256.Sum256(b)
	var receipt *domain.Receipt
	if in.IdempotencyKey != "" {
		receipt = &domain.Receipt{ScopeKey: in.Scope + ":" + in.ProjectKey, Key: in.IdempotencyKey, Hash: hex.EncodeToString(hash[:])}
	}
	id := in.ID
	if id == "" {
		id = uuid()
	}
	m := domain.Memory{ID: id, Scope: in.Scope, ProjectKey: in.ProjectKey, Type: in.Type, Title: in.Title, Content: in.Content, Tags: in.Tags, SourceAgent: in.SourceAgent, SourceReference: in.SourceReference, Status: "active", Evidence: in.Evidence, LastVerifiedAt: in.LastVerifiedAt, ExpiresAt: in.ExpiresAt, IdempotencyKey: in.IdempotencyKey, Handoff: h}
	return s.store.Put(ctx, m, in.ExpectedVersion, receipt, in.SupersedesID, in.SupersedesVersion)
}
func (s *Service) Upsert(ctx context.Context, in domain.UpsertInput) (domain.WriteResult, error) {
	return s.upsert(ctx, in, nil)
}
func (s *Service) budget(requested int) (int, error) {
	max := s.config.MaxResponseChars
	if max == 0 {
		max = 8000
	}
	if requested == 0 {
		return max, nil
	}
	if requested < 1024 || requested > max {
		return 0, domain.Err("validation", "response budget must be 1024..configured maximum")
	}
	return requested, nil
}
func (s *Service) Get(ctx context.Context, in domain.GetInput) (domain.GetResult, error) {
	var out domain.GetResult
	if !idRE.MatchString(in.ID) || in.Offset < 0 || in.Version < 0 {
		return out, domain.Err("validation", "invalid ID, offset or version")
	}
	budget, err := s.budget(in.MaxResponseChars)
	if err != nil {
		return out, err
	}
	m, err := s.store.Get(ctx, in.ID)
	if err != nil {
		return out, err
	}
	if in.Offset > 0 && in.Version == 0 {
		return out, domain.Err("validation", "continuation requires version")
	}
	if in.Version != 0 && in.Version != m.Version {
		return out, domain.Conflict(m.Version)
	}
	content := m.Content
	representation := "text"
	if m.Handoff != nil {
		b, _ := json.Marshal(m.Handoff)
		content = string(b)
		representation = "handoff_json"
	}
	runes := []rune(content)
	if in.Offset > len(runes) {
		return out, domain.Err("validation", "offset exceeds content length")
	}
	out = domain.GetResult{Memory: m, TotalChars: len(runes)}
	if in.Offset == 0 && domain.WireChars(out) <= budget {
		return out, nil
	}
	out.Memory.Handoff = nil
	out.Representation = representation
	// Binary search includes JSON escaping and metadata in the response budget.
	lo, hi := 0, len(runes)-in.Offset
	best := -1
	for lo <= hi {
		n := (lo + hi) / 2
		out.Memory.Content = string(runes[in.Offset : in.Offset+n])
		out.Truncated = in.Offset+n < len(runes)
		out.NextOffset = nil
		if out.Truncated {
			next := in.Offset + n
			out.NextOffset = &next
		}
		if domain.WireChars(out) <= budget {
			best = n
			lo = n + 1
		} else {
			hi = n - 1
		}
	}
	if best < 0 || (best == 0 && in.Offset < len(runes)) {
		return domain.GetResult{}, domain.Err("budget_exceeded", "metadata cannot fit; increase response budget")
	}
	out.Memory.Content = string(runes[in.Offset : in.Offset+best])
	out.Truncated = in.Offset+best < len(runes)
	out.NextOffset = nil
	if out.Truncated {
		next := in.Offset + best
		out.NextOffset = &next
	}
	return out, nil
}
func expression(query string) (string, error) {
	if err := field("query", query, 512, true); err != nil {
		return "", err
	}
	terms := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) })
	if len(terms) == 0 || len(terms) > 32 {
		return "", domain.Err("validation", "query requires 1..32 searchable terms")
	}
	for i, t := range terms {
		terms[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(terms, " AND "), nil
}
func (s *Service) Search(ctx context.Context, in domain.SearchInput) (domain.SearchResult, error) {
	out := domain.SearchResult{Results: []domain.Hit{}}
	if err := domain.CheckInputSize(in); err != nil {
		return out, err
	}
	budget, err := s.budget(in.MaxResponseChars)
	if err != nil {
		return out, err
	}
	if in.Scope == "" {
		in.Scope = "global"
		if in.ProjectKey != "" {
			in.Scope = "project_global"
		}
	}
	switch in.Scope {
	case "global":
		if in.ProjectKey != "" {
			return out, domain.Err("validation", "global scope cannot have a project")
		}
	case "project", "project_global":
		if err = s.project(ctx, in.ProjectKey); err != nil {
			return out, err
		}
	default:
		return out, domain.Err("validation", "invalid search scope")
	}
	if in.Limit == 0 {
		in.Limit = s.config.SearchLimit
		if in.Limit == 0 {
			in.Limit = 5
		}
	}
	if in.Limit < 1 || in.Limit > 20 {
		return out, domain.Err("validation", "limit must be 1..20")
	}
	if len(in.MemoryTypes) > 6 {
		return out, domain.Err("validation", "too many memory types")
	}
	for _, t := range in.MemoryTypes {
		if !memoryType(t) {
			return out, domain.Err("validation", "invalid memory type")
		}
	}
	in.Tags, err = tags(in.Tags)
	if err != nil {
		return out, err
	}
	exp, err := expression(in.Query)
	if err != nil {
		return out, err
	}
	hits, err := s.store.Search(ctx, in, exp)
	if err != nil {
		return out, err
	}
	for _, hit := range hits {
		out.Results = append(out.Results, hit)
		if domain.WireChars(out) > budget {
			out.Results = out.Results[:len(out.Results)-1]
			out.BudgetLimited = true
			break
		}
	}
	for domain.WireChars(out) > budget && len(out.Results) > 0 {
		out.Results = out.Results[:len(out.Results)-1]
		out.BudgetLimited = true
	}
	return out, nil
}
func (s *Service) Archive(ctx context.Context, in domain.ArchiveInput) (domain.WriteResult, error) {
	if !idRE.MatchString(in.ID) || in.ExpectedVersion < 1 {
		return domain.WriteResult{}, domain.Err("validation", "valid ID and expected_version required")
	}
	if err := field("reason", in.Reason, 512, false); err != nil {
		return domain.WriteResult{}, err
	}
	if err := scan(in); err != nil {
		return domain.WriteResult{}, err
	}
	m, err := s.store.Get(ctx, in.ID)
	if err != nil {
		return domain.WriteResult{}, err
	}
	if m.Version != in.ExpectedVersion {
		return domain.WriteResult{}, domain.Conflict(m.Version)
	}
	if m.Status != "active" {
		return domain.WriteResult{}, domain.Err("conflict", "only active records can be archived")
	}
	m.Status = "archived"
	m.ArchiveReason = in.Reason
	return s.store.Put(ctx, m, in.ExpectedVersion, nil, "", 0)
}
func (s *Service) RegisterProject(ctx context.Context, p domain.Project) error {
	if !keyRE.MatchString(p.Key) {
		return domain.Err("validation", "project key must use lowercase letters, digits, dot, underscore or hyphen")
	}
	if err := scan(p); err != nil {
		return err
	}
	for _, f := range []struct {
		name, value string
		max         int
	}{{"remote_url", p.RemoteURL, 512}, {"local_path", p.LocalPath, 1024}, {"organization", p.Organization, 160}, {"description", p.Description, 1000}} {
		if err := field(f.name, f.value, f.max, false); err != nil {
			return err
		}
	}
	if p.RemoteURL != "" {
		u, err := url.Parse(p.RemoteURL)
		if err == nil && u.User != nil {
			if _, present := u.User.Password(); present {
				return domain.Err("sensitive_content", "remote URL must not contain a password")
			}
		}
	}
	if p.LocalPath != "" && !filepath.IsAbs(p.LocalPath) {
		return domain.Err("validation", "local_path must be absolute")
	}
	p.CreatedAt = ""
	p.MemoryCount = 0
	return s.store.RegisterProject(ctx, p)
}
func (s *Service) Projects(ctx context.Context, in domain.ProjectsInput) (domain.ProjectsResult, error) {
	out := domain.ProjectsResult{Projects: []domain.Project{}}
	if in.Register != nil {
		if in.After != "" {
			return out, domain.Err("validation", "register cannot be combined with after")
		}
		if in.Register.LocalPath == "" {
			return out, domain.Err("validation", "register requires local_path")
		}
		if err := s.RegisterProject(ctx, *in.Register); err != nil {
			return out, err
		}
		p, err := s.store.Project(ctx, in.Register.Key)
		out.Projects = append(out.Projects, domain.Project{Key: p.Key, CreatedAt: p.CreatedAt, MemoryCount: p.MemoryCount})
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 10
	}
	if in.Limit < 1 || in.Limit > 20 {
		return out, domain.Err("validation", "limit must be 0..20; omit or use 0 for default 10")
	}
	if in.After != "" && !keyRE.MatchString(in.After) {
		return out, domain.Err("validation", "after must be a project key from next_cursor, up to 80 lowercase letters, digits, dots, underscores or hyphens; omit it for the first page")
	}
	ps, err := s.store.Projects(ctx, in.After, in.Limit+1)
	if err != nil {
		return out, err
	}
	more := len(ps) > in.Limit
	if more {
		ps = ps[:in.Limit]
	}
	for _, p := range ps {
		out.Projects = append(out.Projects, p)
		out.NextCursor = p.Key
		if domain.WireChars(out) > s.config.MaxResponseChars {
			out.Projects = out.Projects[:len(out.Projects)-1]
			more = true
			break
		}
	}
	if len(out.Projects) == 0 && len(ps) > 0 {
		return out, domain.Err("budget_exceeded", "project metadata cannot fit response budget")
	}
	if more {
		out.NextCursor = out.Projects[len(out.Projects)-1].Key
	} else {
		out.NextCursor = ""
	}
	return out, nil
}
func (s *Service) Handoff(ctx context.Context, in domain.HandoffInput) (domain.HandoffResult, error) {
	var out domain.HandoffResult
	if err := domain.CheckInputSize(in); err != nil {
		return out, err
	}
	if err := s.project(ctx, in.ProjectKey); err != nil {
		return out, err
	}
	if in.Operation == "latest" {
		if in.TaskIdentifier != "" && !keyRE.MatchString(in.TaskIdentifier) {
			return out, domain.Err("validation", "invalid task identifier")
		}
		r, err := s.store.Latest(ctx, in.ProjectKey, in.TaskIdentifier)
		return domain.HandoffResult{ID: r.ID, Version: r.Version, UpdatedAt: r.UpdatedAt, TaskIdentifier: r.TaskIdentifier}, err
	}
	if in.Operation != "create" && in.Operation != "update" {
		return out, domain.Err("validation", "operation must be create, update or latest")
	}
	if (in.Operation == "create" && (in.ID != "" || in.ExpectedVersion != 0)) || (in.Operation == "update" && (in.ID == "" || in.ExpectedVersion < 1)) {
		return out, domain.Err("validation", "handoff update requires ID and expected_version")
	}
	if err := validateHandoff(in.Handoff); err != nil {
		return out, err
	}
	for _, id := range in.RelatedMemoryIDs {
		if !idRE.MatchString(id) {
			return out, domain.Err("validation", "invalid related memory ID")
		}
		m, err := s.store.Get(ctx, id)
		if err != nil {
			return out, err
		}
		if m.Scope != "global" && m.ProjectKey != in.ProjectKey {
			return out, domain.Err("validation", "related memory belongs to another project")
		}
	}
	if in.ID != "" {
		old, err := s.store.Get(ctx, in.ID)
		if err != nil {
			return out, err
		}
		if old.Handoff == nil || old.Handoff.TaskIdentifier != in.TaskIdentifier {
			return out, domain.Err("conflict", "handoff task identifier is immutable")
		}
	}
	if err := scan(in); err != nil {
		return out, err
	}
	// Structured fields are authoritative; content is their searchable JSON representation.
	b, _ := json.Marshal(in.Handoff)
	r, err := s.upsert(ctx, domain.UpsertInput{ID: in.ID, ExpectedVersion: in.ExpectedVersion, Scope: "project", ProjectKey: in.ProjectKey, Type: "handoff", Title: in.TaskIdentifier + ": " + short(in.Summary, 70), Content: string(b), SourceAgent: in.SourceAgent, SourceReference: in.SourceReference}, &in.Handoff)
	return domain.HandoffResult{ID: r.ID, Version: r.Version}, err
}

func validateHandoff(in domain.Handoff) error {
	if !keyRE.MatchString(in.TaskIdentifier) {
		return domain.Err("validation", "invalid task identifier")
	}
	if in.Status != "blocked" && in.Status != "in_progress" && in.Status != "completed" {
		return domain.Err("validation", "invalid handoff status")
	}
	if err := field("summary", in.Summary, 1000, true); err != nil {
		return err
	}
	for _, v := range []string{in.Branch, in.CommitReference} {
		if err := field("branch or commit", v, 256, false); err != nil {
			return err
		}
	}
	for _, list := range [][]string{in.CompletedWork, in.Blockers, in.NextActions, in.CommittedChanges, in.UncommittedChanges} {
		if len(list) > 20 {
			return domain.Err("validation", "handoff lists allow at most 20 items")
		}
		for _, v := range list {
			if err := field("handoff item", v, 512, true); err != nil {
				return err
			}
		}
	}
	if len(in.VerificationResults) > 20 || len(in.RelatedMemoryIDs) > 20 {
		return domain.Err("validation", "too many verification results or references")
	}
	for k, v := range in.VerificationResults {
		if err := field("verification key", k, 80, true); err != nil {
			return err
		}
		if err := field("verification result", v, 512, true); err != nil {
			return err
		}
	}
	for _, id := range in.RelatedMemoryIDs {
		if !idRE.MatchString(id) {
			return domain.Err("validation", "invalid related memory ID")
		}
	}
	return nil
}
func short(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
