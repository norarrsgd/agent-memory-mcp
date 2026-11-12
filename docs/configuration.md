# Configuration reference

Configuration is optional JSON at `~/.config/agent-memory/config.json`. A missing default file is allowed; an explicitly selected missing file is an error. Select a file with `--config` or `AGENT_MEMORY_CONFIG`. Unknown JSON keys and multiple JSON values are rejected.

Values are applied in this order: defaults, JSON file, `AGENT_MEMORY_*` environment variables, command flags. Final values are validated after flag overrides. Malformed JSON, unknown keys, and environment values that cannot be parsed remain errors.

```json
{
  "db_path": "/absolute/private/agent-memory/memory.db",
  "search_limit": 5,
  "max_response_chars": 8000,
  "busy_timeout_ms": 5000,
  "max_db_mb": 1024,
  "log_level": "error",
  "backup_dir": "/absolute/private/agent-memory/backups",
  "export_dir": "/absolute/private/agent-memory/exports"
}
```

Paths above are placeholders; replace them with absolute paths under a private application directory. JSON does not expand `~` or shell variables. The default database path is expanded by the program using the OS home directory.

Environment settings are `AGENT_MEMORY_DB_PATH`, `AGENT_MEMORY_SEARCH_LIMIT`, `AGENT_MEMORY_MAX_RESPONSE_CHARS`, `AGENT_MEMORY_BUSY_TIMEOUT_MS`, `AGENT_MEMORY_MAX_DB_MB`, `AGENT_MEMORY_LOG_LEVEL`, `AGENT_MEMORY_BACKUP_DIR`, and `AGENT_MEMORY_EXPORT_DIR`.

Common flags: `--config`, `--db-path`, `--search-limit`, `--max-response-chars`, `--busy-timeout-ms`, and `--log-level`. `backup` and `export` accept `--output`; `import` requires `--input`. All flags follow the command; use `projects register --key stable-key` for registration.

Search count: default 5, range 1–20. Response budget: default 8,000 Unicode characters, configured range 1,024–32,000; a tool call may reduce but cannot raise it. The budget includes tool-result JSON representations, excluding the outer JSON-RPC envelope. Memory content is limited to 64,000 characters; total tool input is limited to 256 KiB, with a small separate frame-envelope allowance. Titles allow 160 characters, source labels 80, references 512, and at most 20 tags of 64 characters each.

Database size limit: `max_db_mb`, default 1,024, range 16–65,536 (configuration file or environment only, no flag). It caps the main database file, converting the limit to pages with the page size in the file header (4,096 bytes for a new database); the WAL file is separate and shrinks at checkpoint. Writes past the limit fail with `storage_full` and change nothing; reads, `export`, and `backup` keep working. A database already larger than the limit stays readable. Archiving does not reclaim space, so raise `max_db_mb` or free disk space to continue.

SQLite busy timeout: default 5,000 ms, range 1–30,000 ms. Startup has an additional one-second deadline allowance. Writes are not retried indefinitely. Log levels are `error`, `info`, and `debug`; diagnostics never contain memory bodies. Info/debug currently include startup diagnostics only.

Backups/exports default to timestamped files in `backups/` and `exports/` next to the database. Outputs must have private immediate parent directories and cannot overwrite existing files. Imports accept only private regular local JSON files, up to 100 MiB, with format version 1.
