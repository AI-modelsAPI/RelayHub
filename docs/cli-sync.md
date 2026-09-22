# CLI Configuration Synchronization

RelayHub provides automatic, non-destructive configuration synchronizers for local coding tools.

## Supported CLIs

| Tool | Target File(s) | Sync Behavior |
|---|---|---|
| **Claude Code** | `~/.claude/settings.json` | Sets `env.ANTHROPIC_BASE_URL` / `env.ANTHROPIC_AUTH_TOKEN` to RelayHub, registers `mcpServers.relayhub`, preserves user MCP servers and hooks |
| **OpenAI Codex** | `~/.codex/config.toml` + `~/.codex/.env` | Registers `model_providers.relayhub` with `env_key = "RELAYHUB_API_KEY"`; the key itself is written to `$CODEX_HOME/.env`, which Codex loads at startup. Approvals and sandbox settings are preserved |
| **Hermes Agent** | `~/.hermes/config.yaml` + `~/.hermes/.env` | Sets `providers.relayhub` in config, keeps the key in `.env` as `HERMES_CUSTOM_RELAYHUB_API_KEY` |

## Preview vs. apply

- `preview` is read-only. It computes the diff and **does not mint a gateway
  key**; the diff shows the placeholder `<<issued-on-apply>>` where the key will
  go.
- `apply` issues one real local API key (`rh_…`) through the key service,
  backs up the existing config, writes config + `.env` atomically, verifies the
  result on disk, and rolls back on failure.
- A syncer that is asked to persist an empty or placeholder key refuses and
  writes nothing — a config that "looks synced" but 401s on first use is worse
  than a refused apply.

## Safety Guarantees

- **Pre-write Backup**: Automatic timestamped backup saved in `<config-dir>/backups/` with SHA256 verification.
- **Atomic Writes**: Uses fsync and atomic rename to prevent partial writes; `.env` files are created `0600`.
- **Symlink Protection**: Refuses to overwrite symlinks.
- **Preservation of Unknown Fields**: Structural parsers retain unrelated keys, hooks, and MCP servers; `.env` edits replace only the RelayHub variable and keep every other line verbatim.
- **Verify**: `verify` checks both the config file and that the key variable is actually present in `.env`.
