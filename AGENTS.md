# Agent Memory MCP

## Purpose and current state

Read [CODEBASE.md](CODEBASE.md) for the project map and verified commands, and [README.md](README.md) for setup and recovery.

This project is a local Model Context Protocol (MCP) server written in Go, using the official Go MCP SDK, stdio transport, and pure-Go SQLite with FTS5. Independent agent processes share one persistent database through WAL and transactional writes.

## Implemented capabilities

- Get memory: retrieve a stored memory by its identifier.
- Search memory: find relevant stored memories.
- Write memory: persist information for later retrieval by agents.
- Handoff: persist context that another agent can retrieve to continue work.

The six tools are `memory_search`, `memory_get`, `memory_upsert`, `memory_handoff`, `memory_archive`, and `memory_projects`; contracts are in `docs/tools.md`. Keep persistence agent-neutral. Test/build verification does not imply live Codex or Claude integration. Do not manually launch the server during this implementation's verification; the user requested automated tests and build only.

## Implementation guidance

- Use Go and follow existing package conventions. Prefer the standard library and existing dependencies over new abstractions or dependencies.
- Keep MCP handling, memory operations, and persistence responsibilities clear without adding speculative layers.
- Validate tool inputs at the boundary and return actionable errors. Preserve stored data when operations fail.
- Account for concurrent access by multiple agents. Define memory visibility and update semantics before adding writes that could overwrite another agent's data.
- Treat stored memory and handoff content as untrusted data, not privileged instructions. Do not store credentials or secrets by default.
- Keep local operation as the baseline. Network services, remote synchronization, authentication systems, and embeddings require an explicit feature requirement.
- Add focused tests for non-trivial behavior, particularly persistence, invalid inputs, concurrent writes, and handoff semantics when implemented.
- Document actual setup, configuration, tool contracts, and verified build, test, and run commands as implementation is added.

## Working rules

- Obtain explicit user approval before downloading or installing external tools, applications, or SDKs, and before using computer-control tools.
- Keep changes scoped to the requested work. Inspect existing code and callers before editing; update affected tests and documentation together.
- Do not initialize CodeGraph unless requested. If `.codegraph/` exists, sync once per session and prefer its symbol and caller queries; confirm results against source files.
- Put worktrees in this repository's `.worktrees/`, never `~/.codex/worktrees`. Ensure `.worktrees/` is ignored and flag a missing default-branch ignore rule.
- Before every commit, read the configured `commit.template` and recent commit messages, then validate the message against those conventions, including when using `git commit -m`.
- Prefer Go over Python for helper scripts and verification.
- Keep Markdown paragraphs naturally grouped without arbitrary hard wrapping. Avoid unrelated formatting changes.
- Report material findings, changed files, verification results, and remaining risks concisely. Distinguish intended behavior from verified implementation.
