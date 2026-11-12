package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/norarrsgd/agent-memory-mcp/internal/domain"
)

func (s *Store) Export(ctx context.Context) (domain.Export, error) {
	out := domain.Export{FormatVersion: 1, ExportedAt: now(), Projects: []domain.Project{}, Memories: []domain.Memory{}, Receipts: []domain.Receipt{}}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, mapError(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT document FROM projects ORDER BY key")
	if err != nil {
		return out, mapError(err)
	}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return out, mapError(err)
		}
		var p domain.Project
		if err = json.Unmarshal(b, &p); err != nil {
			rows.Close()
			return out, domain.Err("storage_failure", "invalid stored project")
		}
		out.Projects = append(out.Projects, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, mapError(err)
	}
	rows, err = tx.QueryContext(ctx, "SELECT document FROM memories ORDER BY id")
	if err != nil {
		return out, mapError(err)
	}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return out, mapError(err)
		}
		var m domain.Memory
		if err = json.Unmarshal(b, &m); err != nil {
			rows.Close()
			return out, domain.Err("storage_failure", "invalid stored memory")
		}
		out.Memories = append(out.Memories, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, mapError(err)
	}
	rows, err = tx.QueryContext(ctx, "SELECT scope_key,key,hash,memory_id,version FROM receipts ORDER BY scope_key,key")
	if err != nil {
		return out, mapError(err)
	}
	for rows.Next() {
		var r domain.Receipt
		if err = rows.Scan(&r.ScopeKey, &r.Key, &r.Hash, &r.ID, &r.Version); err != nil {
			rows.Close()
			return out, mapError(err)
		}
		out.Receipts = append(out.Receipts, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, mapError(err)
	}
	return out, mapError(tx.Commit())
}
func (s *Store) Import(ctx context.Context, data domain.Export) error {
	return s.transaction(ctx, func(c *sql.Conn) error {
		for _, p := range data.Projects {
			b, _ := json.Marshal(p)
			var old []byte
			err := c.QueryRowContext(ctx, "SELECT document FROM projects WHERE key=?", p.Key).Scan(&old)
			if err == nil {
				if string(old) != string(b) {
					return domain.Err("conflict", "import project metadata conflicts")
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = c.ExecContext(ctx, "INSERT INTO projects VALUES(?,?)", p.Key, b); err != nil {
				return err
			}
		}
		for _, m := range data.Memories {
			old, err := get(ctx, c, m.ID)
			if err == nil {
				a, _ := json.Marshal(m)
				b, _ := json.Marshal(old)
				if string(a) != string(b) {
					return domain.Err("conflict", "import memory conflicts")
				}
				continue
			}
			if domain.SafeError(err).Code != "not_found" {
				return err
			}
			if err = save(ctx, c, m, true); err != nil {
				return err
			}
		}
		for _, r := range data.Receipts {
			var old domain.Receipt
			err := c.QueryRowContext(ctx, "SELECT hash,memory_id,version FROM receipts WHERE scope_key=? AND key=?", r.ScopeKey, r.Key).Scan(&old.Hash, &old.ID, &old.Version)
			if err == nil {
				if old.Hash != r.Hash || old.ID != r.ID || old.Version != r.Version {
					return domain.Err("conflict", "import idempotency receipt conflicts")
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = c.ExecContext(ctx, "INSERT INTO receipts VALUES(?,?,?,?,?)", r.ScopeKey, r.Key, r.Hash, r.ID, r.Version); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) Backup(ctx context.Context, destination string) error {
	if err := PrivatePath(destination, true); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return domain.Err("conflict", "backup destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".agent-memory-backup-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	if err = f.Close(); err != nil {
		os.Remove(temporary)
		return err
	}
	defer os.Remove(temporary)
	conn, err := s.write.Conn(ctx)
	if err != nil {
		return mapError(err)
	}
	_, err = conn.ExecContext(ctx, "VACUUM INTO ?", temporary)
	conn.Close()
	if err != nil {
		return mapError(err)
	}
	// Validate before publishing; this opens a private task-owned snapshot only.
	backup, err := Open(temporary, 5000)
	if err != nil {
		return err
	}
	_, checkErr := backup.Doctor(ctx)
	closeErr := backup.Close()
	if checkErr != nil {
		return checkErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(temporary, destination); err != nil {
		return domain.Err("conflict", "backup destination cannot be published without overwrite")
	}
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
