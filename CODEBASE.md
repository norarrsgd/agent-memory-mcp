# Codebase map

This is a local stdio MCP server written in Go. SQLite is authoritative storage; there is no cloud dependency, external LLM, HTTP endpoint, command runner, or automatic repository ingestion.

## Code and boundaries

- `cmd/agent-memory` owns process signals and exit handling.
- `internal/config` loads JSON/environment configuration and validates limits.
- `internal/domain` holds protocol-independent records, inputs, errors, and budget accounting.
- `internal/storage/sqlite` owns private paths, schema migrations, WAL connections, transactions, FTS triggers, backups, and snapshot persistence.
- `internal/memory` owns memory/search validation, secret checks, lifecycle, handoffs, response budgets, and import validation.
- `internal/mcpserver` adapts six typed SDK tools to service methods, plus the `memory_view` tool, which `serve` registers through `WithViewer`; no business logic belongs in transport handlers.
- `internal/snapshot` renders a read-only, self-contained HTML copy of stored memories (embedded `web/` page, CSP by hash, textContent-only rendering, 2000-record and 64 MiB caps) through the memory service and publishes it with `sqlite.WritePrivateFile`.
- `internal/cli` owns commands and operational file input/output, including `view`.
- `internal/claudehook` implements the opt-in stateless `agent-memory hook` command for Claude Code (`SessionStart`, `UserPromptSubmit`, `Stop`). It reads stdin and the session transcript only, never the database, and fails open; `cli` dispatches it before config load and store open.

Dependencies: official MCP Go SDK v1.8.0 and pure-Go modernc SQLite v1.62.0, plus their pinned transitive modules. The installed development SDK is Go 1.27.4 on macOS ARM64. Runtime does not require CGO or network access.

## Verification

Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and `CGO_ENABLED=0 go build -trimpath -o bin/agent-memory ./cmd/agent-memory`. `make check` provides the same workflow; `make fmt` runs `gofmt -w cmd internal`, `make bench` runs the benchmarks, and `make vuln` runs `govulncheck` (needs network, not part of `check`). Under a restricted environment, set `GOCACHE` to a writable cache directory.

Tests use real private temporary SQLite databases. Process tests automatically create temporary MCP test processes and kill an interrupted writer; never use a personal database in tests. Benchmarks are in the memory package and use deterministic 10,000/50,000-record synthetic data.

Do not manually launch the MCP server during this implementation's verification; the user requested tests and build only. Global Codex/Claude configuration changes remain explicitly user-controlled.

## Invariants

Project identity is explicit. Default project searches include global records and never another project's records. Updates require expected versions. Idempotency keys cannot accept a different canonical request. FTS and content changes share a transaction. All persisted text passes validation and secret checks through the service. Storage imports are internal primitives and must only receive service-validated snapshots.

`PrivatePath` is the single shared guard for database, backup, and export destinations (`Open`, `OpenLimited`, `Backup`, `WritePrivateFile`). After the absolute-path, NUL, and file-URI checks and before any filesystem access, it rejects any path that differs from `filepath.Clean(path)`. Callers keep the original spelling the OS will resolve, so a `link/../output` path cannot skip a symlink during inspection. Do not clean paths in callers or add duplicate checks. `canonical_path_test.go` covers a table of `./`, `../`, repeated, and trailing separators (built by concatenation, since `filepath.Join` would erase them), the symlink-then-`..` escape with side-effect assertions, and the writer, open, and backup entry points.

`memory_projects` advertises pagination limits and cursor format in its MCP input schema. Omitted or zero limits use 10; explicit page sizes are capped at 20. Use the returned `next_cursor` as `after`, never a page number or filesystem path. Service validation reports limit and cursor errors separately; keep the schema, validation, and protocol tests consistent.

Project registration is available through the CLI and `memory_projects.register`. Registration returns a compact acknowledgement, not full metadata, so a successful write fits the minimum response budget. Storage cleans new repository paths lexically and checks ownership inside the same write transaction for both registration and import. Imports load ownership once and reject collisions with stored or earlier imported projects atomically. Existing path spellings are compared after cleaning without rewriting historical metadata; symlink and case aliases are not resolved.

Registered projects are capped at `domain.MaxProjects` (10,000) for registration and import alike. Database growth is capped by `max_db_mb` through SQLite `max_page_count`; writes past it return `storage_full` and reads, export, and backup still work. Connections set `trusted_schema=OFF`. Registration and import share `checkRemoteURL` and the `secrets` patterns in `internal/memory/service.go`, so a new pattern needs a positive and a negative case in `internal/memory/secrets_test.go`. `doctor` reports `ancestor_permissions` as a warning, never a failure. `doctor --scan` streams stored documents through the same patterns, prints IDs only, and exits 1 on a match; plain `doctor` never scans.

Preserve current data on migration, import, and backup failure. Do not add silent repair, forced overwrite, physical deletion, raw SQL/filesystem MCP tools, or content-bearing logs. Agent source/evidence labels are claims, not authenticated identities or approvals.

See README for setup and recovery; `docs/architecture.md`, `docs/configuration.md`, `docs/tools.md`, `docs/security.md`, `docs/agent-integration.md`, and `docs/performance.md` provide deeper operational contracts.
