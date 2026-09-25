# RelayHub Docker Deployment

Run RelayHub in a container with complete service persistence.
Full guide (host vs bridge networking, listen-address variables):
`docs/deployment-docker.md`.

## Quick Start with Docker Compose

```bash
docker compose up -d
```

The compose file uses host networking, so the services are on the host's
loopback exactly like a native install:

- **HTTP Proxy**: `127.0.0.1:8787`
- **SOCKS5 Proxy**: `127.0.0.1:8788`
- **AI Gateway**: `127.0.0.1:8789` (OpenAI / Anthropic endpoints)
- **Web UI & Management API**: `http://127.0.0.1:8790`

With a bridge network instead, set `RELAYHUB_HTTP_PROXY_ADDR`,
`RELAYHUB_SOCKS5_ADDR` and `RELAYHUB_GATEWAY_ADDR` to `0.0.0.0:<port>` and
publish the ports on `127.0.0.1` only; the management port cannot be rebound.

## Data Persistence

All state (SQLite database, encrypted master keys, check-in records) is stored inside the `/data` volume:
- `/data/relayhub.db` — SQLite database
- `/data/master.key` — Master key for AES-256-GCM encrypted secrets
- `/data/management.token` — Management API token, generated on first start

## Security Notes

1. The management port `8790` is loopback-only inside the container by design (use `docker exec` to reach it), and it requires the token in `/data/management.token`.
2. The proxies are unauthenticated by default — never publish them beyond the host's loopback.
3. The container process runs as non-root (UID `10001`).
