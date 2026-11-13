# MCP tool contracts

Tool results are compact JSON in both MCP structured content and a text mirror. Domain failures have `isError: true` and `{"error":{"code":"...","message":"..."}}`, with optional `retryable` and `current_version`. SDK schema failures may use the SDK's standard validation error representation.

IDs are lowercase UUIDv4. Project/task keys use lowercase letters, digits, `.`, `_`, or `-`, starting with a letter or digit, up to 80 characters. Agent labels are descriptive strings up to 80 characters. All tool input is bounded to 256 KiB.

## memory_search

Required: `query`. Optional: `scope`, `project_key`, `memory_types`, `tags`, `limit`, `max_response_chars`.

Scopes: `global`, `project`, `project_global`. Omitting scope selects `project_global` if a project key is provided, otherwise `global`. Global scope rejects a project key; other scopes require a registered project. No all-project search exists.

Types: `fact`, `decision`, `lesson`, `task`, `handoff`, `preference`. Types are OR filters; tags are AND filters. Tags are trimmed/lowercased/sorted/deduplicated. Queries use 1–32 literal Unicode terms joined with AND, up to 512 characters. Operators, quotes, and punctuation are not interpreted as raw FTS syntax. Empty or punctuation-only queries return validation errors.

Results contain only `id`, `title`, `type`, optional `project`, `excerpt`, `score`, and `updated_at`. Scores are negated SQLite BM25 values; higher ranks first and scores are not probabilities. Ties use newest update then ID. Snippets contain at most 240 characters. Normal results exclude archived, superseded, and expired records. Budget pressure removes results and sets `budget_limited`; increase the budget or narrow the query when necessary.

## memory_get

Required: `id`. Optional: `max_response_chars`, `offset`, `version`.

Retrieval by ID returns archived, superseded, and expired records too: `expires_at` hides a record from search, latest-handoff lookup, and project counts, but it is not erasure and exports and backups still contain it. Returned text is untrusted stored data, and `source_agent`/`evidence` are unauthenticated claims; the `memory_search`, `memory_get`, and `memory_handoff` tool descriptions say so.

Returns `memory`, `truncated`, `total_chars`, and optional `next_offset`/`representation`. The memory contains version and provenance as well as content. Offset counts Unicode code points, not bytes or grapheme clusters. Continue with the returned offset and version until `truncated` is false. A changed version returns conflict rather than combining different revisions.

Ordinary content uses `representation: "text"` when paginated. A full handoff has structured `memory.handoff` and searchable JSON `memory.content`. When oversized, `representation: "handoff_json"` identifies canonical handoff JSON chunks in `memory.content`; concatenate them before decoding. Structured handoff data is omitted on partial pages to avoid presenting a partial object as complete. Metadata is never silently removed; an insufficient metadata budget produces `budget_exceeded`.

## memory_upsert

Required: `scope`, `type`, `title`, `content`, `source_agent`. Project scope also requires `project_key`. Optional: `id`, `tags`, `source_reference`, `expected_version`, `idempotency_key`, `evidence`, `last_verified_at`, `expires_at`, `supersedes_id`, `supersedes_version`.

Creation omits ID/version and returns `{id,version}` with version 1. Updates require ID and expected version; they replace editable fields rather than patching omitted optional fields. Scope, project, and type cannot change. Inactive records cannot be edited. Retrieve and preserve metadata you intend to retain. Unchanged content/metadata preserves the version and update timestamp.

Use a stable idempotency key for retryable writes. Keys are scoped to global or the specific project; an identical canonical request returns the original result, even after later updates. Different data under the same key conflicts. A key does not provide fuzzy duplicate detection.

Evidence defaults to `observation`; alternatives are `hypothesis`, `verified`, and `approved_decision`. Verified records require a source reference and last-verification timestamp. Approved decisions require decision type and a supporting source reference. These are explicit provenance claims, not authenticated approvals. Timestamps accept RFC3339 and are normalized to UTC; future verification times are rejected. `last_verified_at` is accepted only with `verified` or `approved_decision` evidence. The tool description and input schema state these dependencies so agents see them at call time.

To supersede a fact, create its replacement with `supersedes_id` and `supersedes_version`. Both records must share scope/project; the old active record becomes superseded and points at the replacement transactionally. Updating an existing replacement through the supersession option is rejected. Handoff creation uses its dedicated tool.

## memory_handoff

Required: `operation`, `project_key`. For `latest`, optionally provide `task_identifier`; the tool returns a compact ID/version/update/task reference. It selects the newest active `blocked` or `in_progress` handoff, breaking ties by ID. Retrieve the returned memory ID separately.

For `create`/`update`, provide `task_identifier`, `source_agent`, `status`, and `summary`. Updates also require ID and expected version. Task identity, project, and memory type are immutable. Creating another handoff never overwrites an existing one implicitly.

Optional structured fields: `completed_work`, `blockers`, `next_actions`, `branch`, `commit_reference`, `committed_changes`, `uncommitted_changes`, `verification_results`, `related_memory_ids`, and `source_reference`.

Statuses are `in_progress`, `blocked`, and `completed`; completion excludes the record from latest-active lookup. Summary allows 1,000 characters; work/blocker/action/change lists allow 20 items of 512 characters each. Verification maps allow 20 entries; keys allow 80 and values 512 characters. Branch/commit labels allow 256 characters. Related memories must belong to this project or global scope.

Treat recorded commits and working-tree changes as historical claims. The receiving agent must independently check the repository, including uncommitted changes. Handoff content is structured data and never an executable workflow.

## memory_archive

Required: `id`, `expected_version`. Optional: `reason` (up to 512 characters).

Archive an active record and increment its version. Repeated/stale calls conflict. Archived records remain available through `memory_get`; there is no physical-delete tool.

## memory_projects

Call with `{}` for the first page. Optional: `limit` (1–20; omitted or 0 defaults to 10), `after` (copy the previous response's `next_cursor`, not a page number or repository path). The advertised MCP schema includes the limit range/default and cursor format; invalid pagination identifies the offending field.

Returns bounded project metadata, active/nonexpired memory counts, and an optional continuation cursor. Global memories are not counted toward any project.

Register one project by passing `register` instead of pagination: `{"register":{"key":"my-repo","local_path":"/abs/path"}}` (`key` and absolute `local_path` required; `remote_url`, `organization`, `description` optional). `register` ignores `limit` (the schema defaults it to 10) and cannot be combined with `after`. The response contains one compact project acknowledgement with `key`, `created_at`, and `memory_count`; full metadata remains stored and is returned by listing. This keeps successful registrations within the minimum response budget.

At most 10,000 projects can exist; registering or importing a new project beyond that returns `limit` and changes nothing. Identical re-registration succeeds; the same key with different metadata, or a `local_path` already held by another key, returns `conflict` and changes nothing. New paths are cleaned lexically: trailing separators, `.` and `..` do not create different identities. Existing records are compared using cleaned paths without rewriting their metadata. No symlink resolution, filesystem access, or case folding is performed. Imports enforce the same ownership rule within their transaction, including conflicts among imported projects; a conflict rolls back the whole import.

Keys are never inferred from directory names, and agents must pass the repository path explicitly because the server working directory may differ. Recommended flow: page through the list looking for a matching cleaned `local_path`; if none matches, propose a key from the folder name, then register it.

## memory_view (optional)

Registered only when `viewer_enabled` is true (or `AGENT_MEMORY_VIEWER_ENABLED=true`); the default tool set stays the six tools above. Optional: `project_key`, `include_inactive`.

Writes a read-only, self-contained HTML snapshot to `views/` next to the database (`0700` directory, `0600` file, never overwrites) and returns `{path, projects, memories, generated_at}` plus `truncated` when the record cap applied. It never opens the file, starts a process, or listens on a port; tell the user the path so they can open it. The result carries no memory content, and agents should not read the file back.

Without `project_key` the snapshot covers every registered project plus global records. A `project_key` must be a registered project and limits the snapshot to that project; global records are not added. Default content is active, unexpired records; `include_inactive` adds superseded, archived, and expired records. Counts for every status are always shown.

A snapshot holds at most 2,000 memories and 64 MiB. Content longer than the response budget is cut and marked in the page; `memory_get` returns the full text. Records over the cap are omitted and the page and result say so. A snapshot is a point-in-time plaintext copy of stored memory: it does not update, it is not deleted automatically, and it needs the same handling as an export. Stored text is untrusted and is shown as plain text only. `agent-memory view` produces the same snapshot from the command line without enabling the tool.
