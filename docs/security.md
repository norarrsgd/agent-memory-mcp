# Security and sensitive-content policy

Run under your own OS account, on a local filesystem, with a private application directory. The server exposes stdio only, opens no network listener, sends no telemetry, and makes no external API requests. Build-time module downloads are separate from runtime behavior.

## What may be stored

Store compact engineering context whose retention is appropriate for the project: constraints, rationale, milestones, failed approaches, next steps, and preferences. Do not store credentials, authentication tokens, private keys, password-bearing connection strings, sensitive client data, conversation transcripts, or raw debugging dumps.

Credential detection rejects recognizable private-key headers, common GitHub/OpenAI/Anthropic/Slack/AWS token forms, JWT-like strings, credential assignments, and URL userinfo containing passwords. Checks cover memory/provenance text, handoffs, project metadata, archive reasons, and JSON imports. Errors do not echo the matched value.

Detection is defense in depth. It has false positives and cannot identify every encoded secret, provider token, personal datum, or sensitive organizational fact. Agents and users remain responsible for content selection. Do not encode a secret to bypass a rejection.

## Trust boundary

Any process running as the same OS user with database access can inspect or change this data. Source-agent names are descriptive and unauthenticated. Project filters prevent accidental retrieval across projects; they do not implement authorization. Use a separate OS account or database when stronger separation is required.

Stored text is untrusted data. Text such as “ignore previous instructions” is stored/retrieved as ordinary content and never evaluated. The server has no SQL execution tool, arbitrary filesystem tool, command runner, or mechanism for memory to change permissions or configuration. Repository paths and commit references are metadata, not executable instructions.

Explicit evidence labels do not prove a human approved a decision. Agents must verify references against authoritative sources.

## Files and recovery

Application directories are private (`0700`); database, WAL/SHM, backups, and exports must be private (`0600`). Symlink components, URI database paths, and nonregular targets are rejected. Existing unsafe permissions cause a failure, not automatic broad permission changes. SQLite databases and JSON exports are unencrypted: use OS disk encryption and appropriate backup access controls.

Path validation does not defend against a malicious same-account process racing to replace files. SQLite is not a sandbox for attacker-crafted database files. Binary restore accepts trusted backups only; JSON import validates the portable record model before transactional insertion.

Backup publishes a validated SQLite snapshot without overwriting a destination. JSON import skips identical records and aborts conflicts; there is no force option. Preserve database/WAL state before recovery. Archives are retention, not secure erasure; old data may remain in backups and historical filesystem blocks.

No automatic Git or cloud synchronization exists. Never publish private exports or personal databases. `.gitignore` excludes conventional runtime artifacts, but explicitly chosen destinations remain the user's responsibility.
