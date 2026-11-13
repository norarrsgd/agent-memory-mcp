# Shared Agent Memory

`agent-memory` is a local Go MCP server for persistent knowledge shared by Codex, Claude Code, and other MCP clients. Each client launches its own stdio process against one SQLite database. No cloud service, LLM, embeddings, HTTP listener, Docker, or Python is required at runtime.

Shared memory stores facts, decisions, lessons, tasks, handoffs, and preferences independently of an agent's native conversation history. Search returns compact references; agents retrieve complete records only when useful. Memory is a contextual index: current code, approved documentation, and authoritative task records take precedence over historical notes.

## Build and test

Use Go 1.27.4 or newer with the pinned module dependencies available. Initial module downloads require internet access; the executable does not.

```sh
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o bin/agent-memory ./cmd/agent-memory
```

`make check` runs these checks. `make bench` runs the synthetic 10,000/50,000-record workloads. Tests use private temporary databases. Protocol tests automatically launch short-lived test processes; no server is left running.

## Register a project

```sh
./bin/agent-memory projects register --key my-app --description 'Application engineering'
./bin/agent-memory projects list
./bin/agent-memory doctor
./bin/agent-memory status
```

Project keys are explicit and stable when repository directories move. Optional remote URL, local path, and organization are metadata only; registration never reads a repository or runs Git. Agents can also register through `memory_projects` with `register`, which returns a compact acknowledgement while retaining full metadata for listing. Re-registering identical metadata succeeds; conflicting registration fails instead of silently changing identity. Repository paths are cleaned lexically, and registration/import reject a path owned by another key. Symlinks and filesystem case aliases are not resolved.

Default database: `~/.local/share/agent-memory/memory.db`. Its application directory must be private (`0700`), and files must be private (`0600`). A configured override must be an absolute local filesystem path with a private immediate parent. Resolve filesystem aliases before supplying a path; symlink components are rejected.

## Connect agents

See [agent integration](docs/agent-integration.md) for user-scoped examples and minimal instructions. Both clients must use the same absolute executable and database paths. Existing global configuration and instruction files are never modified by this project.

`agent-memory serve` is the stdio entry point used by MCP clients. It is not a daemon or an HTTP service; do not run it in a terminal expecting interactive output. Diagnostics use stderr exclusively.

## Tools

- `memory_search`: compact ranked references, default five and maximum 20 results.
- `memory_get`: targeted retrieval with explicit continuation and version checks.
- `memory_upsert`: creation, version-checked updates, deterministic idempotency, verification metadata, and supersession.
- `memory_handoff`: structured create/update and deterministic latest-active lookup.
- `memory_archive`: version-checked archival without deletion.
- `memory_projects`: bounded discovery and active-memory counts.

A project search includes global memories by default. Explicit project-only searches are available. No project search includes another project's records. Global-only searches do not include project memories. Search does not return full bodies.

See [tool contracts](docs/tools.md), [configuration](docs/configuration.md), [architecture](docs/architecture.md), [security](docs/security.md), [performance](docs/performance.md), and [verification report](docs/verification.md). Synthetic tool arguments are in `examples/memories.json`.

## View memories

```sh
./bin/agent-memory view --project my-app
```

`view` writes a read-only, self-contained HTML page (projects, then memory counts by type, status and handoff status, then each memory) to `views/` next to the database and prints its path; open the file in any browser. Nothing listens on a port and nothing keeps running. Flags: `--project`, `--include-inactive`, `--output`. The page is a point-in-time plaintext copy of stored memory; treat it like an export. Agents can request the same snapshot through the always-registered `memory_view` tool, which returns only the file path. See [tool contracts](docs/tools.md) and [security](docs/security.md).

## Back up and restore

```sh
./bin/agent-memory backup
./bin/agent-memory export
```

These create timestamped private snapshots under the database directory's `backups/` and `exports/`. Use `--output /absolute/private/path` to choose a destination. Existing output files are never overwritten. Backup uses SQLite `VACUUM INTO`, validates the resulting database, then publishes it; it does not copy an active database file.

JSON export preserves IDs, versions, project associations, provenance, lifecycle relationships, structured handoffs, and idempotency receipts. `agent-memory import --input /absolute/private/export.json` validates and merges atomically. Identical records are skipped; conflicting records abort the whole import. Import is not a force-overwrite command.

For binary restore:

1. Stop every MCP client process using this database.
2. Preserve the existing database together with any `-wal` and `-shm` files in a private recovery directory. Do not discard a live WAL.
3. Run `agent-memory doctor --db-path /absolute/private/backup.db` to validate the trusted backup.
4. Restore the standalone backup as `memory.db` in a new private directory, with mode `0600`; do not reuse stale WAL/SHM files from another database.
5. Run `doctor --db-path` against the restored database and point both agents to it before restarting them.

Keep backups and exports out of public repositories. Never open an untrusted SQLite database as a restore source; use validated JSON import for portable records. Automated tests cover snapshot restoration and repeated JSON imports.

## Troubleshoot

- `busy`: another process holds a write lock. Retry after it completes; inspect unexpected long-running clients rather than disabling locking.
- `conflict`: retrieve the current version and reconcile the change. Reusing an idempotency key with different content also conflicts.
- `validation`: inspect required fields, scope, registration, timestamps, limits, or private path permissions.
- `storage_full`: the database reached `max_db_mb` (or the disk is full). Raise `max_db_mb` in configuration or free disk space; archiving does not reclaim space. Nothing was written.
- `limit`: 10,000 projects are registered. Projects cannot be removed. To drop a mistaken or squatted key, export, remove that project from the JSON offline (and any of its memories, which an import rejects if their project is missing), and import into a fresh database. This is covered by a test.
- `sensitive_content`: remove credential-like content from the input. Do not bypass the check by encoding the secret.
- `budget_exceeded`: request a larger budget within the configured ceiling. Retrieval never silently discards essential metadata.
- `schema` or integrity failure: preserve the database and its associated state, stop writers, and recover from a validated backup. The server does not reset or rebuild data automatically.

All commands support `--help`. Exit codes: `0` success, `1` operational failure, `2` argument/configuration failure. Tool errors are structured with safe messages and retryability where applicable.

## Limits

FTS5 provides lexical relevance, not semantic understanding. Characters are not model tokens, and MCP tool definitions plus duplicated text/structured output consume context. Actual token savings depend on agents searching selectively and recording meaningful events.

Protocol tests prove interoperability with the official SDK; they do not prove that Codex or Claude will choose the right memories. Live agent integration has not been performed. Secret detection is heuristic, data is not encrypted, and project boundaries are logical rather than authentication boundaries.
