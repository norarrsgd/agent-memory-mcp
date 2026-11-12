CREATE TABLE projects (
 key TEXT PRIMARY KEY,
 document TEXT NOT NULL CHECK(json_valid(document))
);
CREATE TABLE memories (
 rowid INTEGER PRIMARY KEY,
 id TEXT NOT NULL UNIQUE,
 scope TEXT NOT NULL CHECK(scope IN ('global','project')),
 project_key TEXT REFERENCES projects(key),
 type TEXT NOT NULL CHECK(type IN ('fact','decision','lesson','task','handoff','preference')),
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
CREATE TABLE tags (
 memory_id TEXT NOT NULL REFERENCES memories(id),
 tag TEXT NOT NULL,
 PRIMARY KEY(memory_id,tag)
);
CREATE INDEX tags_filter ON tags(tag,memory_id);
CREATE INDEX memories_project_status ON memories(project_key,status,type,updated_at,id);
CREATE INDEX memories_scope_status ON memories(scope,status,type);
CREATE INDEX memories_handoff ON memories(project_key,handoff_task,handoff_status,updated_at,id) WHERE type='handoff' AND status='active';
CREATE TABLE receipts (
 scope_key TEXT NOT NULL,
 key TEXT NOT NULL,
 hash TEXT NOT NULL,
 memory_id TEXT NOT NULL REFERENCES memories(id),
 version INTEGER NOT NULL,
 PRIMARY KEY(scope_key,key)
);
CREATE VIRTUAL TABLE memories_fts USING fts5(title,content,tags_text,content='memories',content_rowid='rowid',tokenize='unicode61');
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
