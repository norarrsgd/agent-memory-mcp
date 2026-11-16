# Claude Code instructions

Project rules are shared with other agents and live in `AGENTS.md`; the project map and verified commands are in `CODEBASE.md`. Edit those files, not this one, for anything that is not Claude-specific.

@AGENTS.md
@CODEBASE.md

## Claude Code specifics

- The `mcp__agent-memory__*` tools may be deferred. Load their schemas with ToolSearch before the first memory call, and set `source_agent` to `claude`.
- `agent-memory hook` is the opt-in Claude Code hook (`SessionStart`, `UserPromptSubmit`, `Stop`). Its design and limits are in `docs/agent-integration.md`; the Claude-side configuration examples are in `examples/claude/`. Never edit the user's `~/.claude` settings or `~/.claude.json` from this repo.
- Claude permissions for this repo go in `.claude/settings.local.json`; do not create `.claude/settings.json`.
