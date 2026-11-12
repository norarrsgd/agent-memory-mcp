# Configure Codex and Claude Code

Build the executable first and register stable project keys. Both clients launch independent stdio processes with the same database path. No background daemon is necessary.

## Verification boundary

Installed Codex CLI 0.167.0 was inspected using `codex --version`, `codex mcp --help`, and `codex mcp add --help`. These confirm the documented command syntax. Claude Code is not installed; its examples follow the official documentation at https://code.claude.com/docs/en/mcp and are not locally verified.

Automated official-SDK client tests cover initialization, tool discovery, invocation, structured errors, stdio, and cross-process visibility. Neither client has been launched against this server, and no global configuration has been modified. Per the development constraint, no manual server launch is part of verification.

## Codex

`examples/codex/config.toml` is a stanza to merge into `~/.codex/config.toml`. Replace placeholder paths with absolute paths. Existing server names/configuration must be preserved. An equivalent user-approved registration command is:

```sh
codex mcp add agent-memory --env AGENT_MEMORY_DB_PATH="$HOME/.local/share/agent-memory/memory.db" -- "$PWD/bin/agent-memory" serve
```

Run that command from the built repository root only after deciding to modify your user-level Codex configuration. `$PWD` is expanded to an absolute path when registered; rebuild at that path or update configuration when moving the executable. `codex mcp get agent-memory` inspects the registered entry.

Review `examples/codex/AGENTS.md` and append the shared-memory section to `~/.codex/AGENTS.md` yourself. Do not replace existing instructions.

## Claude Code

Claude's user-scoped MCP entries are stored in `~/.claude.json`; they are not the same as global instructions in `~/.claude/CLAUDE.md`. `examples/claude/mcp.json` shows the MCP object to merge after replacing paths.

Once Claude is installed, inspect `claude --version` and `claude mcp add --help` before using this documented registration command:

```sh
claude mcp add --transport stdio --scope user agent-memory --env AGENT_MEMORY_DB_PATH="$HOME/.local/share/agent-memory/memory.db" -- "$PWD/bin/agent-memory" serve
```

This command changes user configuration and is provided for review, not executed automatically. Inspect the entry with `claude mcp get agent-memory`. Review and append `examples/claude/CLAUDE.md` to your existing global Claude instructions without overwriting them.

The server also sends a short usage convention in the MCP `instructions` field at initialize (search at task start, upsert before the final response, handoff before stopping), so clients that surface server instructions apply it without user-level files. Rebuild `bin/agent-memory` and restart Claude Code to pick up changes. This is not locally verified against Claude Code.

Claude Code may defer MCP tool schemas, so the agent must load them (ToolSearch) before its first memory call; the sample instructions say so. Instructions remain advisory. To enforce the loop, configure Claude Code hooks yourself (for example a `SessionStart` hook that injects a search reminder and a `Stop` hook that blocks the final response until memory was written or skipped explicitly); hooks live in user settings and are not provided or modified by this repository.

## Retrieval and handoff workflow

1. Select an explicitly registered project key using `memory_projects` and project context. Never equate similarly named directories automatically.
2. Search once at the start of each task, scoped to its topic. Project searches include global preferences; use explicit project-only scope when appropriate.
3. Retrieve selected IDs; follow version-guarded continuation only when necessary.
4. Verify stored conclusions against current code, approved documentation, and task records.
5. Persist meaningful changes with provenance and accurate evidence labels, not routine commands or conversations.
6. Before an interruption, record a structured handoff including verification gaps and separate committed/uncommitted work.
7. The receiving agent requests `memory_handoff` with `operation: "latest"`, project, and optional task, then retrieves its ID and independently checks the current repository.

Agents choose when to call tools. MCP configuration cannot guarantee recall, persistence, or appropriate relevance. Tool definitions and result metadata consume tokens; selective retrieval determines whether shared memory reduces overall context use. These instruction snippets implement an event-driven convention, not automatic lifecycle hooks or transcript ingestion.
