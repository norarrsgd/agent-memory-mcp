# Memory viewer UI

Status: implemented (revision 5: `memory_view` is always registered; the `viewer_enabled` opt-in described below was removed at the user's request). The standalone desktop app was dropped; the deliverable is the `view` command and the optional `memory_view` tool. Every mention of the desktop app, its toolkit options and phase 4 below is historical and was not built.

Decisions: no hosted service; v1 read-only; `memory_view` writes a static HTML snapshot and returns its path (no browser, no command run); desktop app is a pure-Go launcher. Revision 3: the user then required macOS, Windows and Linux support, and chose a browser launcher over a webview or native toolkit, because every native-window option needs cgo or per-OS runtime libraries (WebView2 on Windows 10, WebKitGTK on Linux). No webview dependency was added.

Revision 2 removes the loopback HTTP viewer from revision 1. Decision from the user: no hosted service; everything is an on-demand tool. Also decided: v1 is read-only.

## Goal

A read-only UI over the shared memory DB with a three-level drill-down:

1. Projects list, from the projects table plus a synthetic "Global" entry for `scope=global` records.
2. Selecting a project shows its memory counts first (by type, status, handoff status).
3. Selecting a count opens the matching memories, and selecting a memory shows full detail (content, tags, provenance, handoff structure).

Two on-demand delivery paths, neither of which leaves a process running or opens a port:

- A standalone Go desktop app the user launches when they want it. It opens the DB read-only in-process and exits when the window closes.
- An optional MCP tool so a user can ask an agent "show me this project's memories". It produces a self-contained snapshot file and returns its path.

## Constraints from the repo that shape the design

- `CODEBASE.md` and `AGENTS.md`: no HTTP endpoint, network listener, or command runner. Revision 2 stays inside this: no listener anywhere, and the MCP server runs no external command.
- Server binary builds with `CGO_ENABLED=0` and pure-Go SQLite. A native GUI toolkit needs cgo on macOS, so the desktop app must be a separate binary and the server's build and dependency graph must not change.
- Stored content is untrusted. Both UIs are new rendering surfaces for it: render as plain text only, never as HTML, markdown, or script.
- There is no list-memories-by-project API today. Search needs a query and `memory_projects` returns counts only. A read-only list method is required.
- Installing a GUI toolkit needs explicit user approval (global rule). The desktop phase is gated on that.

## Architecture

```
internal/memory/            new read-only service methods: ProjectSummary, ListMemories (reuse Get for detail)
internal/storage/sqlite/    new read-only queries backing those methods
internal/snapshot/          builds one self-contained HTML file from service data (html/template + embed.FS, no network, no external assets)
internal/mcpserver/         optional memory_view tool, registered only when enabled
internal/cli/               new `agent-memory view` command (same snapshot generator, for use without an agent)
cmd/agent-memory-desktop/   separate binary, native window, direct read-only service access
```

Everything goes through `memory.Service`, never SQL, matching the existing "no business logic in transport" rule.

### Data layer additions (shared by both paths)

- `Store.ProjectSummary(ctx, key)`: counts by `type` and `status`, plus handoff counts by `handoff_status`. `key=""` selects global scope. Expired records are excluded from active counts to match `memory_projects`.
- `Store.List(ctx, ListInput)`: `project` (or global), optional `type`, `status` (default active), `handoff_status`, keyset cursor on `(updated_at DESC, id)`, `limit` capped at 50. Returns compact references (id, title, type, updated_at, version, status), no content.
- Detail uses the existing `Get` with its response budget and offset pagination. Archived, superseded, and expired records are hidden by default and shown only through an explicit "include inactive" toggle.
- Read-only on the existing read pool. No writes in v1.

### Screens (identical in both paths)

1. Projects: filterable list of every registered project with active memory count, plus a "Global" row.
2. Project summary: key, path, remote, description, then count tiles by type, then status, with a handoff row (in_progress / blocked / completed). Each tile is clickable.
3. Memory list: filtered by the clicked tile; rows show type, title, version, updated, evidence badge.
4. Memory detail: full content, tags, source reference, evidence (labelled "unauthenticated claim"), last verified, expiry, supersession links, structured handoff view. Related memory IDs link to their detail.

## Path A: MCP tool `memory_view` (snapshot file)

- Opt-in: registered only when configuration `viewer.enabled=true` (default false), so the six-tool contract is unchanged for everyone else. Documented as an optional seventh tool in `docs/tools.md`.
- Input: optional `project_key` (validated with the existing key rule, must be registered), optional `include_inactive`. No input produces a snapshot of all projects.
- Behaviour: reads through the service, writes one HTML file under the database directory's `views/` folder (private `0700` directory, `0600` file, never overwrites, same private-path checks as `export`), returns `{path, project, memory_count, generated_at}`. The tool never opens it and never runs a command. The agent tells the user the path and the user opens it.
- The snapshot is a static point-in-time copy with embedded JSON and a tiny inline-free script (CSP meta `default-src 'none'; script-src 'sha256-...'`), rendering with `textContent` only. No network, no external assets.
- Because it is a copy of memory content on disk, it is subject to the same handling as exports: private permissions, kept out of repositories, secrets scan already applied at write time. Add a retention note and an optional `agent-memory view --prune` later if wanted (not in v1).
- Snapshot size is bounded: per-memory content truncated at the existing detail budget with a visible "truncated, use memory_get for the full text" marker; project-wide snapshots capped by record count with a stated limit.

## Path B: standalone desktop app

Dropped (revision 4). The user required macOS, Windows and Linux support. Every native-window option needs cgo or per-OS runtime libraries (WebView2 on Windows 10, WebKitGTK on Linux), a pure-Go browser launcher was built and verified on macOS, and the user then chose to drop the desktop app entirely. The launcher and its `--open` flag were removed so the project still runs no subprocess. Users open the snapshot file themselves. The toolkit options below were considered and not used.

- `cmd/agent-memory-desktop`, launched by the user, opens the DB read-only through the same config loader and private-path checks, renders the screens above natively, exits when closed. WAL lets it run alongside any MCP processes. No listener, nothing resident.
- Toolkit options, which need your approval because they are new dependencies and require cgo:
  1. Fyne: pure-Go API, mature, widgets for lists and trees; cgo/OpenGL on macOS and a large dependency tree.
  2. Gio: leaner, immediate-mode, more layout code to write by hand.
  3. A system-webview wrapper (webview_go) that renders the snapshot HTML in a window: the smallest dependency and reuses Path A's page verbatim, but it is a webview even though nothing is hosted.
- Separate Makefile target and `go.mod` handling so `make check` and the `CGO_ENABLED=0` server build stay untouched. If the desktop module's dependencies would pollute `go.mod`, put it in its own nested module under `cmd/agent-memory-desktop/`.

## Phases and order of operations

1. Data layer: `Store.ProjectSummary`, `Store.List`, service wrappers, domain types. Tests: real temp SQLite, counts exclude expired and inactive by default, global scope, cursor stability, invalid cursor/limit errors, no cross-project leakage.
2. `internal/snapshot` and `agent-memory view`. Tests: hostile content (`<script>`, HTML/markdown in title, content, tags, handoff fields) round-trips as data and the generated file contains no executable injected markup; private permissions; never overwrites; size caps and truncation marker.
3. `memory_view` tool behind `viewer.enabled`. Tests: not registered by default, registered when enabled, input validation, returned path is inside `views/`, schema consistent with `docs/tools.md`.
4. Desktop app after you choose the toolkit and approve the dependency. Build and smoke-test only.
5. Docs: `CODEBASE.md`, `README.md`, `docs/architecture.md`, `docs/security.md`, `docs/tools.md`, `docs/configuration.md`, `docs/agent-integration.md` (when to call `memory_view`).

Phases 1 to 3 are about 10 to 14 files, so each lands as its own commit.

## Verification

- `go test ./...`, `go test -race ./...`, `go vet ./...`, `CGO_ENABLED=0 go build -trimpath -o bin/agent-memory ./cmd/agent-memory` (must still pass without cgo).
- Per-phase tests above. No manual server launch, consistent with `AGENTS.md`.
- A visual check means opening the snapshot or desktop window and taking a screenshot. That needs browser or computer control, which needs your explicit approval at that time.

## Risks and open points

- Snapshot files are plaintext copies of memory content outside the DB. They live in a private directory, but they accumulate. Mitigation is documented handling now, pruning later.
- The snapshot is stale the moment it is written. The page shows its generated timestamp. The desktop app reads live data.
- Cross-project browsing in one view is a new aggregation surface, although the DB is already readable by the same user.
- Archive, supersede, and edit actions are out of scope for v1 (need expected-version handling and a trustworthy write path).

## Rollback

All additions are new files or additive read-only methods, and the tool is off by default. Reverting the commits restores the current six-tool server. No schema migration, so no data rollback.
