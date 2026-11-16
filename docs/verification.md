# Implementation and verification report

Verified on 2026-11-12 with Go 1.27.4, darwin/arm64, Apple M4 Pro. This report distinguishes automated protocol tests from live agent integration.

## Delivered files

- `cmd/agent-memory/`: executable entry point and signal handling.
- `internal/config/`, `internal/domain/`: configuration, record/tool inputs, safe errors, and budget accounting.
- `internal/storage/sqlite/`, including `migrations/001_initial.sql`: embedded atomic migration, WAL connections, transactional writes, FTS5, health checks, snapshots, and backup.
- `internal/memory/`: independent validation, secret checks, scoped search, Unicode retrieval, lifecycle, handoffs, import validation, integration tests, and benchmarks.
- `internal/mcpserver/`, `internal/cli/`: six official-SDK tools plus `memory_view`, stdio framing limits, operational commands, and automated process tests.
- `go.mod`, `go.sum`, `Makefile`, `.gitignore`: pinned build dependencies and local workflows.
- `README.md`, `CODEBASE.md`, updated `AGENTS.md`, and `docs/`: architecture, contracts, security, configuration, recovery, integration, measurements, and this report.
- `examples/`: proposed Codex/Claude configuration, minimal agent instructions, and synthetic memory/handoff tool inputs.
- `bin/agent-memory`: built macOS ARM64 executable, 17,910,306 bytes, CGO disabled; ignored as a generated artifact.

There was no existing Go implementation at the initial verification. No personal database or user-level agent settings were created or modified.

## Dependencies and architecture

Direct dependencies are `github.com/modelcontextprotocol/go-sdk v1.8.0` for official MCP transport/schema/client interoperability and `modernc.org/sqlite v1.62.0` for pure-Go SQLite/FTS5 without a C toolchain. Required transitive modules are pinned in `go.mod`/`go.sum`; their download was explicitly approved. No new external application, Go SDK, Python tooling, or runtime service was installed.

MCP and CLI adapt a shared memory/search service. The service owns validation and lifecycle; SQLite storage owns transactions, migrations, indexes, and snapshots. SQLite coordinates independent client processes; no process-local mutex is the sole concurrency mechanism. `memory_search`, `memory_get`, `memory_upsert`, `memory_handoff`, `memory_archive`, and `memory_projects` are implemented.

## Executed verification

All final checks below completed successfully. `GOCACHE` pointed at this checkout's ignored `.cache/go-build` because the managed environment restricts the normal cache. `GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off` were applied to final checks so cached dependencies were used without downloads.

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o bin/agent-memory ./cmd/agent-memory
```

All seven packages with tests passed (`claudehook`, `cli`, `config`, `mcpserver`, `memory`, `snapshot`, `storage/sqlite`); the entry-point and domain packages have no standalone test files. Their behavior is exercised through CLI, service, and protocol tests. There are 78 top-level test functions, including subprocess helpers (counted 2026-11-17, when `go test -count=1 ./...` was rerun and passed; the other checks above were last run on 2026-11-12). `gofmt -l cmd internal` produced no output; whitespace and temporary-diagnostic searches found no issues.

Automated coverage includes initialization/persistence, migration checksum/newer-schema rejection, atomic migration rollback, corrupted-database preservation, CAS conflicts, idempotency, archival/supersession/expiration, FTS updates/ranking/filtering, literal malformed queries, Unicode/budgets/continuation, handoffs, secret rejection including JSON snippets, canonical import validation, repeated/conflicting imports, backups/restoration during independent writes, busy handling, concurrent initialization/read/write, interrupted-transaction recovery, and deliberately inconsistent FTS detection.

Official-SDK MCP tests verify discovery, invocation, validation/errors, structured results, documented example payloads, and minimal latest-handoff input. Automated stdio process tests prove a write from one process is readable by another, competing same-version updates have exactly one winner, and committed state remains readable after process termination and restart. All test servers use temporary databases and terminate with the tests. No manual server launch was performed.

Benchmarks executed with 10,000/50,000 initial records and 100 operations per workload. At 50,000 records, global/project search p95 was 3.043/2.932 ms and ID retrieval averaged 8.94 µs. Full latency, payload, database, WAL, memory, and query-plan evidence is in `performance.md`; these synthetic measurements are not production guarantees.

## Review findings and corrections

A read-only review returned two findings: JSON escaping could conceal credential assignments, and imports discarded normalization before persistence. Both were reproduced with failing tests, fixed in shared validation, and all final checks were rerun.

Additional testing exposed journal-mode startup contention, FTS health checks on query-only readers, latest-handoff schema requirements, flag precedence/explicit-zero handling, and missing structured import validation. These paths now have regression coverage. Test fixture directories were corrected to use private permissions so migration/corruption tests reach the intended database operations rather than failing only path validation.

## User-controlled integration and remaining limits

Build/install location, project registration, and user-level MCP/instruction changes remain user actions. Review `agent-integration.md` and `examples/`; preserve existing configuration. The documented Codex and Claude Code commands and configuration were not run against a live client, and neither client was connected to the server during verification.

Tests/build pass with module downloads disabled and the implementation has no outbound runtime feature. No dedicated firewall isolation test or idle-server RSS measurement was performed. Protocol compatibility does not guarantee agent recall or relevance.

Secret detection is heuristic, SQLite/export data is unencrypted, evidence labels are unauthenticated claims, and projects share an OS-account trust boundary. JSON import is capped at 100 MiB and validates in memory; use trusted SQLite backup/restore for larger complete stores. No automatic secret cleanup, schema repair, force import, or physical deletion exists.

Semantic search, automatic ingestion/consolidation, server-side lifecycle hooks (only the opt-in, stateless `agent-memory hook` for Claude Code exists; see `agent-integration.md`), GitHub synchronization, HTTP, dashboards, cross-device synchronization, and automatic Git/cloud workflows are intentionally deferred.
