-- Adds the finding and discovery memory types. SQLite cannot alter a CHECK constraint, so the table is rebuilt
-- with its rowids preserved; the external-content FTS index stays valid because rowid and indexed text are unchanged.
-- The runner disables foreign keys around this transaction and verifies them with foreign_key_check before commit.
CREATE TABLE memories_new (
 rowid INTEGER PRIMARY KEY,
 id TEXT NOT NULL UNIQUE,
 scope TEXT NOT NULL CHECK(scope IN ('global','project')),
 project_key TEXT REFERENCES projects(key),
 type TEXT NOT NULL CHECK(type IN ('fact','decision','lesson','task','handoff','preference','finding','discovery')),
 title TEXT NOT NULL,
 content TEXT NOT NULL,
 tags_text TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('active','superseded','archived')),
 expires_at TEXT,
 version INTEGER NOT NULL CHECK(version > 0),
 handoff_task TEXT,
 handoff_status TEXT,
 document TEXT NOT NULL CHECK(json_valid(document)),
 CHECK((scope='global' AND project_key IS NULL) OR (scope='project' AND project_key IS NOT NULL))
);
INSERT INTO memories_new(rowid,id,scope,project_key,type,title,content,tags_text,updated_at,status,expires_at,version,handoff_task,handoff_status,document)
 SELECT rowid,id,scope,project_key,type,title,content,tags_text,updated_at,status,expires_at,version,handoff_task,handoff_status,document FROM memories;
DROP TABLE memories;
ALTER TABLE memories_new RENAME TO memories;
CREATE INDEX memories_project_status ON memories(project_key,status,type,updated_at,id);
CREATE INDEX memories_scope_status ON memories(scope,status,type);
CREATE INDEX memories_handoff ON memories(project_key,handoff_task,handoff_status,updated_at,id) WHERE type='handoff' AND status='active';
CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
 INSERT INTO memories_fts(rowid,title,content,tags_text) VALUES(new.rowid,new.title,new.content,new.tags_text);
END;
CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
 INSERT INTO memories_fts(memories_fts,rowid,title,content,tags_text) VALUES('delete',old.rowid,old.title,old.content,old.tags_text);
END;
CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
 INSERT INTO memories_fts(memories_fts,rowid,title,content,tags_text) VALUES('delete',old.rowid,old.title,old.content,old.tags_text);
 INSERT INTO memories_fts(rowid,title,content,tags_text) VALUES(new.rowid,new.title,new.content,new.tags_text);
END;
