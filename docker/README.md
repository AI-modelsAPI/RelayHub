# RelayHub Docker Deployment

Run RelayHub in container with complete service persistence.

## Quick Start with Docker Compose

```bash
docker compose up -d
```

Service mappings:
- **HTTP Proxy**: `127.0.0.1:8787`
- **SOCKS5 Proxy**: `127.0.0.1:8788`
- **AI Gateway**: `127.0.0.1:8789` (OpenAI / Anthropic endpoints)
- **Web UI & Management API**: `http://127.0.0.1:8790`

## Data Persistence

All state (SQLite database, encrypted master keys, check-in records) is stored inside the `/data` volume:
- `/data/relayhub.db` — SQLite database
- `/data/master.key` — Master key for AES-256-GCM encrypted secrets

## Security Notes

1. Management port `8790` is bound to `127.0.0.1` by default to prevent unauthorized access from public networks.
2. The container process runs as non-root (UID `10001`).
