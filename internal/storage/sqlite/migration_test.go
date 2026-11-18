package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrationAddsMemoryTypes upgrades a version 1 database and checks that data, FTS, child rows and foreign keys survive.
func TestMigrationAddsMemoryTypes(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "private"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private", "db")
	v1, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(v1)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		string(v1),
		"CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)",
		"INSERT INTO schema_migrations VALUES(1,'" + hex.EncodeToString(sum[:]) + "')",
		`INSERT INTO projects VALUES('alpha','{}')`,
		`INSERT INTO memories(id,scope,project_key,type,title,content,tags_text,updated_at,status,version,document) VALUES('m1','project','alpha','lesson','Upgrade title','zebrafish content','t','2026-01-01T00:00:00Z','active',1,'{}')`,
		`INSERT INTO tags VALUES('m1','t')`,
		`INSERT INTO receipts VALUES('s','k','h','m1',1)`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("INSERT INTO memories(id,scope,type,title,content,tags_text,updated_at,status,version,document) VALUES('x','global','finding','t','c','','2026-01-01T00:00:00Z','active',1,'{}')"); err == nil {
		t.Fatal("version 1 schema should reject finding")
	}
	db.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err = os.Chmod(path+suffix, 0600); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	s, err := Open(path, 50)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := s.read.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("SELECT max(version) FROM schema_migrations"); n != SchemaVersion {
		t.Fatalf("schema version %d", n)
	}
	if n := count("SELECT count(*) FROM memories WHERE id='m1' AND type='lesson'"); n != 1 {
		t.Fatalf("memory lost: %d", n)
	}
	if n := count("SELECT count(*) FROM tags") + count("SELECT count(*) FROM receipts"); n != 2 {
		t.Fatalf("child rows lost: %d", n)
	}
	if n := count("SELECT count(*) FROM memories_fts JOIN memories m ON m.rowid=memories_fts.rowid WHERE memories_fts MATCH 'zebrafish'"); n != 1 {
		t.Fatalf("fts lost: %d", n)
	}
	for _, kind := range []string{"finding", "discovery"} {
		if _, err = s.write.ExecContext(ctx, "INSERT INTO memories(id,scope,type,title,content,tags_text,updated_at,status,version,document) VALUES(?,'global',?,'t','narwhal','','2026-01-01T00:00:00Z','active',1,'{}')", kind, kind); err != nil {
			t.Fatalf("%s rejected after migration: %v", kind, err)
		}
	}
	if n := count("SELECT count(*) FROM memories_fts JOIN memories m ON m.rowid=memories_fts.rowid WHERE memories_fts MATCH 'narwhal'"); n != 2 {
		t.Fatalf("triggers not recreated: %d", n)
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO memories(id,scope,type,title,content,tags_text,updated_at,status,version,document) VALUES('bad','global','nonsense','t','c','','2026-01-01T00:00:00Z','active',1,'{}')"); err == nil {
		t.Fatal("unknown type must still be rejected")
	}
	var fk int
	if err = s.write.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys not restored: %d %v", fk, err)
	}
}
