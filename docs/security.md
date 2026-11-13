# Security and sensitive-content policy

Run under your own OS account, on a local filesystem, with a private application directory. The server exposes stdio only, opens no network listener, sends no telemetry, and makes no external API requests. Build-time module downloads are separate from runtime behavior.

## What may be stored

Store compact engineering context whose retention is appropriate for the project: constraints, rationale, milestones, failed approaches, next steps, and preferences. Do not store credentials, authentication tokens, private keys, password-bearing connection strings, sensitive client data, conversation transcripts, or raw debugging dumps.

Credential detection rejects recognizable private-key headers, common GitHub/OpenAI/Anthropic/Slack/AWS/Google/Stripe/npm/PyPI token forms, JWT-like strings, `Bearer` tokens, credential assignments, including environment-style names such as `DB_PASSWORD` or `GITHUB_TOKEN` (the broad names `secret`, `token`, `account_key`, `secret_access_key`, `shared_access_key`, and `credentials` need a value of 16 or more characters that contains a digit, so an all-letter secret after them is not caught), Azure shared access signatures, and URL userinfo containing a password or a 24-or-more-character token. Remote URLs with a password, even an empty one, are rejected by registration and import alike; short usernames such as `git@` or an Azure DevOps organization are allowed. Prose such as `token: 42` or `secret: key-vault-name-prod` is accepted; a value with 16 or more characters including a digit after those names is rejected, so rephrase instead of quoting. A remote URL whose userinfo cannot be parsed (for example a password containing a space) is rejected too. Checks cover memory/provenance text, handoffs, project metadata, archive reasons, and JSON imports. Errors do not echo the matched value. Detection runs when text is written, so records stored before a pattern existed are not checked again automatically: `agent-memory doctor --scan` rescans every stored memory and project with the current patterns and prints their IDs and keys (never content) under `audit`. It is read-only and exits 1 when any record matches, so scripts can gate on it. To clear a hit, update an active record with new content through `memory_upsert`. Archived and superseded records cannot be edited and archiving keeps their content, so such a record keeps failing JSON import (the error names the record); binary backup and restore are not scanned and remain the way to carry it forward.

Detection is defense in depth. It has false positives and cannot identify every encoded secret, provider token, personal datum, or sensitive organizational fact. Agents and users remain responsible for content selection. Do not encode a secret to bypass a rejection.

## Trust boundary

Any process running as the same OS user with database access can inspect or change this data. Source-agent names are descriptive and unauthenticated. Project filters prevent accidental retrieval across projects; they do not implement authorization. Use a separate OS account or database when stronger separation is required.

Stored text is untrusted data. Text such as “ignore previous instructions” is stored/retrieved as ordinary content and never evaluated. The server has no SQL execution tool, arbitrary filesystem tool, command runner, or mechanism for memory to change permissions or configuration. Repository paths and commit references are metadata, not executable instructions.

Global memory is shared by every project, so one poisoned global `preference` or `fact` reaches every project's default search. Prefer project scope, and review global records when an agent behaves unexpectedly. The server cannot stop a model from obeying stored text; it only states the untrusted-data contract in the descriptions of the tools that return text. Records with an `expires_at` in the past are hidden from search but remain retrievable by ID and present in exports and backups.

Explicit evidence labels do not prove a human approved a decision. Agents must verify references against authoritative sources.

## Snapshots

`agent-memory view` and the `memory_view` tool write an HTML copy of stored memories. It is plaintext, point-in-time and never deleted automatically, so it has the same exposure as a JSON export: it is created `0600` in a private `views/` directory, never overwrites a file, and must stay out of repositories and shared folders. The page is a new rendering surface for untrusted text. It displays stored content only through `textContent`, embeds its data with `<`, `>` and `&` escaped, makes no network requests, and carries a Content-Security-Policy that denies everything except its own script and style by SHA-256 hash. `source_agent` and `evidence` are labelled as unauthenticated claims on the page. Nothing opens a snapshot or runs a command; the user opens the file. A snapshot is not an authorization boundary: anyone who can read the file sees everything in it.

## Dependency advisories

`make vuln` runs `govulncheck ./...`, which needs the tool on `PATH` and network access to the Go vulnerability database; it is not part of `make check`. On 2026-11-12 govulncheck v1.8.0 scanned all 19 modules and the Go 1.27.4 standard library against a database updated 2026-11-11 and reported no vulnerabilities. Re-run it before releases and after dependency changes; a clean result covers only advisories known on that date.

## Files and recovery

Platform note: private-path validation compares Unix permission bits and syncs directories. That is verified on macOS only. The code cross-compiles for Windows and Linux, but Windows reports different permission bits and cannot sync a directory, so the database is expected to fail its private-path check there; Windows is unsupported until that is addressed.

Application directories are private (`0700`); database, WAL/SHM, backups, and exports must be private (`0600`). Symlink components, URI database paths, and nonregular targets are rejected. Existing unsafe permissions cause a failure, not automatic broad permission changes. SQLite databases and JSON exports are unencrypted: use OS disk encryption and appropriate backup access controls.

Path validation does not defend against a malicious same-account process racing to replace files. It checks the immediate parent, not every ancestor, so `agent-memory doctor` reports `ancestor_permissions`: `warn` lists directories above the private one that group or others can write to without the sticky bit, because anyone who can write to them could rename the private directory away. It is a warning, never a failure. SQLite is not a sandbox for attacker-crafted database files. Connections set `trusted_schema=OFF`, so triggers and views stored in a database file cannot call unsafe functions or virtual tables; in the bundled build `load_extension` is disabled and `readfile`, `writefile`, and `fts3_tokenizer` are absent, so this is defense in depth. The build also compiles in the `sqlite_dbpage` and `dbstat` virtual tables; probes showed that views and triggers cannot use either with `trusted_schema` on or off (SQLite marks them direct-only), so a hostile schema has no route to raw page access or the filesystem. The remaining restore risk is a parser flaw in SQLite itself, which only a trusted backup source avoids. Binary restore accepts trusted backups only; JSON import validates the portable record model before transactional insertion.

Backup publishes a validated SQLite snapshot without overwriting a destination. JSON import skips identical records and aborts conflicts; there is no force option. Preserve database/WAL state before recovery. Archives are retention, not secure erasure; old data may remain in backups and historical filesystem blocks.

No automatic Git or cloud synchronization exists. Never publish private exports or personal databases. `.gitignore` excludes conventional runtime artifacts, but explicitly chosen destinations remain the user's responsibility.
