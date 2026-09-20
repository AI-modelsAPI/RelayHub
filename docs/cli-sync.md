# CLI Configuration Synchronization

RelayHub provides automatic, non-destructive configuration synchronizers for local coding tools.

## Supported CLIs

| Tool | Target File | Sync Behavior |
|---|---|---|
| **Claude Code** | `~/.claude/settings.json` | Configures `ANTHROPIC_BASE_URL` to RelayHub, preserves user MCP and hooks |
| **OpenAI Codex** | `~/.codex/config.toml` | Registers `model_providers.relayhub`, preserves approvals and sandbox settings |
| **Hermes Agent** | `~/.hermes/config.yaml` + `.env` | Sets `providers.relayhub` in config, keeps local API key in `.env` |

## Safety Guarantees

- **Pre-write Backup**: Automatic timestamped backup saved in `<config-dir>/backups/` with SHA256 verification.
- **Atomic Writes**: Uses fsync and atomic rename to prevent partial writes.
- **Symlink Protection**: Refuses to overwrite symlinks.
- **Preservation of Unknown Fields**: Structural parsers retain unrelated keys, hooks, and MCP servers.
