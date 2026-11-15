package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
	sqliteDriver "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const SchemaVersion = 1

// DefaultMaxDBMB is the database size ceiling used when no limit is configured.
const DefaultMaxDBMB = 1024

// defaultPageSize is SQLite's default, used for a database file that does not exist yet.
const defaultPageSize = 4096

// databasePageSize reads the page size from an existing database header, so the page-count cap matches the file.
func databasePageSize(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return defaultPageSize
	}
	defer f.Close()
	var header [18]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return defaultPageSize
	}
	size := int(binary.BigEndian.Uint16(header[16:18]))
	if size == 1 {
		return 65536
	}
	if size < 512 || size&(size-1) != 0 {
		return defaultPageSize
	}
	return size
}

type Store struct {
	read  *sql.DB
	write *sql.DB
	Path  string

	maxDBMB int
}
type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

// PrivatePath validates destinations without modifying unrelated parent directories.
func PrivatePath(path string, create bool) error {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "file:") {
		return domain.Err("validation", "path must be an absolute filesystem path")
	}
	// The OS resolves the original spelling, so a lexically cleaned inspection could skip a symlink
	// (link/../x). Callers keep the original path, hence only canonical spellings are accepted.
	if path != filepath.Clean(path) {
		return domain.Err("validation", "path must be canonical: dot segments, repeated separators and trailing separators are unsupported")
	}
	parent := filepath.Dir(path)
	// Refuse symlink components, including dangling links.
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return domain.Err("validation", "symlink paths are not supported")
		}
		if err != nil && !os.IsNotExist(err) {
			return domain.Err("storage_failure", "cannot inspect storage path")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if create {
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
	}
	info, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return domain.Err("validation", "storage parent directory must be private (0700)")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return domain.Err("validation", "storage file must be regular and private (0600)")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func Open(path string, busyMS int) (*Store, error) {
	return OpenLimited(path, busyMS, DefaultMaxDBMB)
}

// OpenLimited opens the store and caps database growth at maxDBMB mebibytes; writes beyond it fail with storage_full.
func OpenLimited(path string, busyMS, maxDBMB int) (*Store, error) {
	if busyMS < 1 || busyMS > 30000 {
		return nil, domain.Err("validation", "invalid busy timeout")
	}
	if maxDBMB < 16 || maxDBMB > 65536 {
		return nil, domain.Err("validation", "invalid database size limit")
	}
	if err := PrivatePath(path, true); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		err = f.Close()
	} else if os.IsExist(err) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := PrivatePath(path+suffix, false); err != nil {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	for _, p := range []string{"busy_timeout(" + fmt.Sprint(busyMS) + ")", "foreign_keys(1)", "synchronous(FULL)", "trusted_schema(0)", "max_page_count(" + fmt.Sprint(maxDBMB*1024*1024/databasePageSize(path)) + ")"} {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	w, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	s := &Store{write: w, Path: path, maxDBMB: maxDBMB}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(busyMS+1000)*time.Millisecond)
	defer cancel()
	if err = retryStartup(ctx, func() error { _, err := w.ExecContext(ctx, "PRAGMA journal_mode=WAL"); return err }); err != nil {
		s.Close()
		return nil, mapError(err)
	}
	if err = retryStartup(ctx, func() error { return s.migrate(ctx) }); err != nil {
		s.Close()
		return nil, err
	}
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	r, err := sql.Open("sqlite", u.String())
	if err != nil {
		s.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	s.read = r
	return s, nil
}

// Journal-mode transitions can return SQLITE_BUSY without invoking SQLite's busy handler.
func retryStartup(ctx context.Context, fn func() error) error {
	for {
		err := fn()
		if err == nil || domain.SafeError(mapError(err)).Code != "busy" {
			return err
		}
		select {
		case <-ctx.Done():
			return mapError(err)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Store) Close() error {
	var a, b error
	if s.read != nil {
		a = s.read.Close()
	}
	if s.write != nil {
		b = s.write.Close()
	}
	return errors.Join(a, b)
}
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Err("not_found", "record not found")
	}
	var e *domain.Error
	if errors.As(err, &e) {
		return e
	}
	var se *sqliteDriver.Error
	if errors.As(err, &se) && se.Code()&255 == 13 {
		return domain.Err("storage_full", "database is full: raise max_db_mb or free disk space; archiving does not reclaim space")
	}
	if errors.As(err, &se) && (se.Code()&255 == 5 || se.Code()&255 == 6) {
		return &domain.Error{Code: "busy", Message: "database busy; retry later", Retryable: true}
	}
	return domain.Err("storage_failure", "database operation failed")
}
func (s *Store) transaction(ctx context.Context, fn func(*sql.Conn) error) error {
	c, err := s.write.Conn(ctx)
	if err != nil {
		return mapError(err)
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return mapError(err)
	}
	defer func() { _, _ = c.ExecContext(context.Background(), "ROLLBACK") }()
	if err = fn(c); err != nil {
		return mapError(err)
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return mapError(err)
}
func (s *Store) migrate(ctx context.Context) error {
	return s.transaction(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)"); err != nil {
			return err
		}
		var version int
		if err := c.QueryRowContext(ctx, "SELECT coalesce(max(version),0) FROM schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version > SchemaVersion {
			return domain.Err("schema", "database schema is newer than this executable")
		}
		for i := 1; i <= SchemaVersion; i++ {
			b, err := migrations.ReadFile(fmt.Sprintf("migrations/%03d_initial.sql", i))
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			checksum := hex.EncodeToString(h[:])
			if i <= version {
				var stored string
				if err = c.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=?", i).Scan(&stored); err != nil {
					return err
				}
				if stored != checksum {
					return domain.Err("schema", "migration checksum mismatch")
				}
				continue
			}
			if _, err = c.ExecContext(ctx, string(b)); err != nil {
				return domain.Err("schema", "schema migration failed; database preserved")
			}
			if _, err = c.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(?,?)", i, checksum); err != nil {
				return err
			}
		}
		return nil
	})
}
func get(ctx context.Context, q querier, id string) (domain.Memory, error) {
	var b []byte
	var m domain.Memory
	if err := q.QueryRowContext(ctx, "SELECT document FROM memories WHERE id=?", id).Scan(&b); err != nil {
		return m, mapError(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, domain.Err("storage_failure", "invalid stored record")
	}
	return m, nil
}
func (s *Store) Get(ctx context.Context, id string) (domain.Memory, error) {
	return get(ctx, s.read, id)
}
func (s *Store) ProjectExists(ctx context.Context, key string) error {
	var n int
	return mapError(s.read.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE key=?", key).Scan(&n))
}
func (s *Store) RegisterProject(ctx context.Context, p domain.Project) error {
	p = normalizeProject(p)
	return s.transaction(ctx, func(c *sql.Conn) error {
		var old []byte
		err := c.QueryRowContext(ctx, "SELECT document FROM projects WHERE key=?", p.Key).Scan(&old)
		if err == nil {
			var existing domain.Project
			if json.Unmarshal(old, &existing) != nil {
				return domain.Err("storage_failure", "invalid project")
			}
			p.CreatedAt = existing.CreatedAt
			b, _ := json.Marshal(p)
			normalized, _ := json.Marshal(normalizeProject(existing))
			if string(normalized) == string(b) {
				return nil
			}
			return domain.Err("conflict", "project already registered with different metadata")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		count, err := projectCount(ctx, c)
		if err != nil {
			return err
		}
		if count >= domain.MaxProjects {
			return errProjectLimit()
		}
		paths, err := projectPaths(ctx, c)
		if err != nil {
			return err
		}
		if err := claimProjectPath(paths, p); err != nil {
			return err
		}
		p.CreatedAt = now()
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, "INSERT INTO projects VALUES(?,?)", p.Key, b)
		return err
	})
}

func projectCount(ctx context.Context, c *sql.Conn) (int, error) {
	var count int
	err := c.QueryRowContext(ctx, "SELECT count(*) FROM projects").Scan(&count)
	return count, err
}

func errProjectLimit() error { return domain.Err("limit", "project limit reached") }

func normalizeProject(p domain.Project) domain.Project {
	if p.LocalPath != "" {
		p.LocalPath = filepath.Clean(p.LocalPath)
	}
	return p
}

func projectPaths(ctx context.Context, c *sql.Conn) (map[string]string, error) {
	rows, err := c.QueryContext(ctx, "SELECT key, coalesce(json_extract(document,'$.local_path'),'') FROM projects ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make(map[string]string)
	for rows.Next() {
		var key, path string
		if err := rows.Scan(&key, &path); err != nil {
			return nil, err
		}
		if path != "" {
			path = filepath.Clean(path)
			if _, exists := paths[path]; !exists {
				paths[path] = key
			}
		}
	}
	return paths, rows.Err()
}

func claimProjectPath(paths map[string]string, p domain.Project) error {
	if p.LocalPath == "" {
		return nil
	}
	if other, exists := paths[p.LocalPath]; exists && other != p.Key {
		return domain.Err("conflict", "local_path is already registered under project key "+other)
	}
	paths[p.LocalPath] = p.Key
	return nil
}

func (s *Store) Project(ctx context.Context, key string) (domain.Project, error) {
	var b []byte
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT p.document,(SELECT count(*) FROM memories m WHERE m.project_key=p.key AND m.status='active' AND (m.expires_at IS NULL OR m.expires_at>?)) FROM projects p WHERE p.key=?`, now(), key).Scan(&b, &n)
	if err != nil {
		return domain.Project{}, mapError(err)
	}
	var p domain.Project
	if json.Unmarshal(b, &p) != nil {
		return domain.Project{}, domain.Err("storage_failure", "invalid project")
	}
	p.MemoryCount = n
	return p, nil
}
func (s *Store) Projects(ctx context.Context, after string, limit int) ([]domain.Project, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT p.document,(SELECT count(*) FROM memories m WHERE m.project_key=p.key AND m.status='active' AND (m.expires_at IS NULL OR m.expires_at>?)) FROM projects p WHERE p.key>? ORDER BY p.key LIMIT ?`, now(), after, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	ps := []domain.Project{}
	for rows.Next() {
		var b []byte
		var n int
		if err = rows.Scan(&b, &n); err != nil {
			return nil, mapError(err)
		}
		var p domain.Project
		if json.Unmarshal(b, &p) != nil {
			return nil, domain.Err("storage_failure", "invalid project")
		}
		p.MemoryCount = n
		ps = append(ps, p)
	}
	return ps, mapError(rows.Err())
}
func save(ctx context.Context, c *sql.Conn, m domain.Memory, insert bool) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var pk, expiry, task, status any
	if m.ProjectKey != "" {
		pk = m.ProjectKey
	}
	if m.ExpiresAt != "" {
		expiry = m.ExpiresAt
	}
	if m.Handoff != nil {
		task = m.Handoff.TaskIdentifier
		status = m.Handoff.Status
	}
	args := []any{m.Scope, pk, m.Type, m.Title, m.Content, strings.Join(m.Tags, " "), m.UpdatedAt, m.Status, expiry, m.Version, task, status, b, m.ID}
	if insert {
		_, err = c.ExecContext(ctx, `INSERT INTO memories(scope,project_key,type,title,content,tags_text,updated_at,status,expires_at,version,handoff_task,handoff_status,document,id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	} else {
		_, err = c.ExecContext(ctx, `UPDATE memories SET scope=?,project_key=?,type=?,title=?,content=?,tags_text=?,updated_at=?,status=?,expires_at=?,version=?,handoff_task=?,handoff_status=?,document=? WHERE id=?`, args...)
	}
	if err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "DELETE FROM tags WHERE memory_id=?", m.ID); err != nil {
		return err
	}
	for _, tag := range m.Tags {
		if _, err = c.ExecContext(ctx, "INSERT INTO tags VALUES(?,?)", m.ID, tag); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Put(ctx context.Context, m domain.Memory, expected int64, receipt *domain.Receipt, supersedes string, supersedesVersion int64) (domain.WriteResult, error) {
	var result domain.WriteResult
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if receipt != nil {
			var hash string
			err := c.QueryRowContext(ctx, "SELECT hash,memory_id,version FROM receipts WHERE scope_key=? AND key=?", receipt.ScopeKey, receipt.Key).Scan(&hash, &result.ID, &result.Version)
			if err == nil {
				if hash != receipt.Hash {
					return domain.Err("conflict", "idempotency key reused with different input")
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		old, err := get(ctx, c, m.ID)
		insert := domain.SafeError(err).Code == "not_found"
		if err != nil && !insert {
			return err
		}
		if insert {
			if expected != 0 {
				return domain.Err("not_found", "record not found")
			}
			m.Version = 1
			m.CreatedAt = now()
		} else {
			if expected < 1 || old.Version != expected {
				return domain.Conflict(old.Version)
			}
			if old.Scope != m.Scope || old.ProjectKey != m.ProjectKey || old.Type != m.Type {
				return domain.Err("validation", "scope, project and type are immutable")
			}
			if old.Status != "active" {
				return domain.Err("conflict", "inactive record cannot be updated")
			}
			m.CreatedAt = old.CreatedAt
			m.UpdatedAt = old.UpdatedAt
			m.Version = old.Version
			a, _ := json.Marshal(m)
			b, _ := json.Marshal(old)
			if string(a) == string(b) && supersedes == "" {
				result = domain.WriteResult{ID: old.ID, Version: old.Version}
				return nil
			}
			m.Version++
		}
		m.UpdatedAt = now()
		if supersedes != "" {
			if !insert {
				return domain.Err("validation", "supersession requires a new replacement record")
			}
			old, err := get(ctx, c, supersedes)
			if err != nil {
				return err
			}
			if old.Version != supersedesVersion {
				return domain.Conflict(old.Version)
			}
			if old.Status != "active" || old.Scope != m.Scope || old.ProjectKey != m.ProjectKey {
				return domain.Err("validation", "superseded record must be active and in the same scope")
			}
			old.Status = "superseded"
			old.SupersededBy = m.ID
			old.Version++
			old.UpdatedAt = m.UpdatedAt
			if err = save(ctx, c, old, false); err != nil {
				return err
			}
		}
		if err = save(ctx, c, m, insert); err != nil {
			return err
		}
		result = domain.WriteResult{ID: m.ID, Version: m.Version}
		if receipt != nil {
			_, err = c.ExecContext(ctx, "INSERT INTO receipts VALUES(?,?,?,?,?)", receipt.ScopeKey, receipt.Key, receipt.Hash, result.ID, result.Version)
		}
		return err
	})
	return result, err
}
func (s *Store) Latest(ctx context.Context, project, task string) (domain.LatestResult, error) {
	var r domain.LatestResult
	query := `SELECT id,version,updated_at,handoff_task FROM memories WHERE project_key=? AND type='handoff' AND status='active' AND handoff_status IN ('in_progress','blocked') AND (expires_at IS NULL OR expires_at>?)`
	args := []any{project, now()}
	if task != "" {
		query += " AND handoff_task=?"
		args = append(args, task)
	}
	query += " ORDER BY updated_at DESC,id ASC LIMIT 1"
	err := s.read.QueryRowContext(ctx, query, args...).Scan(&r.ID, &r.Version, &r.UpdatedAt, &r.TaskIdentifier)
	return r, mapError(err)
}
func (s *Store) Search(ctx context.Context, in domain.SearchInput, expression string) ([]domain.Hit, error) {
	query := `SELECT m.id,m.title,m.type,coalesce(m.project_key,''),snippet(memories_fts,1,'','', ' … ',32),-bm25(memories_fts,5.0,1.0,2.0),m.updated_at FROM memories_fts JOIN memories m ON m.rowid=memories_fts.rowid WHERE memories_fts MATCH ? AND m.status='active' AND (m.expires_at IS NULL OR m.expires_at>?)`
	args := []any{expression, now()}
	switch in.Scope {
	case "global":
		query += " AND m.scope='global'"
	case "project":
		query += " AND m.project_key=?"
		args = append(args, in.ProjectKey)
	case "project_global":
		query += " AND (m.project_key=? OR m.scope='global')"
		args = append(args, in.ProjectKey)
	}
	if len(in.MemoryTypes) > 0 {
		query += " AND m.type IN (" + strings.TrimRight(strings.Repeat("?,", len(in.MemoryTypes)), ",") + ")"
		for _, t := range in.MemoryTypes {
			args = append(args, t)
		}
	}
	for _, tag := range in.Tags {
		query += " AND EXISTS(SELECT 1 FROM tags WHERE memory_id=m.id AND tag=?)"
		args = append(args, tag)
	}
	query += " ORDER BY bm25(memories_fts,5.0,1.0,2.0),m.updated_at DESC,m.id ASC LIMIT ?"
	args = append(args, in.Limit)
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	hits := []domain.Hit{}
	for rows.Next() {
		var h domain.Hit
		if err = rows.Scan(&h.ID, &h.Title, &h.Type, &h.Project, &h.Excerpt, &h.Score, &h.UpdatedAt); err != nil {
			return nil, mapError(err)
		}
		r := []rune(h.Excerpt)
		if len(r) > 240 {
			h.Excerpt = string(r[:240])
		}
		hits = append(hits, h)
	}
	return hits, mapError(rows.Err())
}
func (s *Store) Doctor(ctx context.Context) (map[string]any, error) {
	var check string
	// FTS5 quick_check on query_only readers can misreport segment corruption.
	if err := s.write.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return nil, mapError(err)
	}
	if check != "ok" {
		return nil, domain.Err("storage_failure", "database integrity check failed")
	}
	rows, err := s.read.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, mapError(err)
	}
	bad := rows.Next()
	rowErr := rows.Err()
	rows.Close()
	if bad || rowErr != nil {
		return nil, domain.Err("storage_failure", "foreign key check failed")
	}
	err = s.transaction(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "INSERT INTO memories_fts(memories_fts,rank) VALUES('integrity-check',1)")
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"schema_version": SchemaVersion, "integrity": "ok", "fts5": "ok", "foreign_keys": "ok", "ancestor_permissions": ancestorPermissions(s.Path)}, nil
}

// ancestorPermissions reports directories above the private parent that others can write to without the sticky bit,
// since they could rename the private directory away. It warns instead of failing: valid setups such as
// user-private groups legitimately use group-writable home directories.
func ancestorPermissions(path string) string {
	var open []string
	for dir := filepath.Dir(filepath.Dir(path)); ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
			open = append(open, dir)
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if len(open) == 0 {
		return "ok"
	}
	return "warn: group/world-writable without sticky bit: " + strings.Join(open, ", ")
}
func (s *Store) Status(ctx context.Context) (map[string]any, error) {
	var projects, memories int
	var mode string
	if err := s.read.QueryRowContext(ctx, "SELECT count(*) FROM projects").Scan(&projects); err != nil {
		return nil, mapError(err)
	}
	if err := s.read.QueryRowContext(ctx, "SELECT count(*) FROM memories").Scan(&memories); err != nil {
		return nil, mapError(err)
	}
	if err := s.read.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"schema_version": SchemaVersion, "projects": projects, "memories": memories, "journal_mode": mode}, nil
}
