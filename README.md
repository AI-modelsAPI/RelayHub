# RelayHub

> Local-first AI Gateway, Transparent Proxy, and Check-in Aggregator.

RelayHub unifies multiple AI relays and public endpoints behind a single, resilient local interface. It automates daily quota check-ins across built-in and custom providers, routes OpenAI and Anthropic API traffic with circuit breaker protection, and syncs configurations directly into Claude Code, Codex, and Hermes Agent.

## Key Capabilities

- **Unified AI Gateway (`127.0.0.1:8789`)**: Compatible with OpenAI (`/v1/chat/completions`, `/v1/responses`, `/v1/embeddings`) and Anthropic (`/v1/messages`).
- **Transparent Proxies**: Local HTTP/HTTPS CONNECT proxy (`:8787`) and SOCKS5 proxy (`:8788`).
- **Six-Layer Decoupling**: Provider -> Channel -> Model -> ProviderModel -> ModelGroup -> Route.
- **Automated Check-ins**: Built-in adapters for AgentRouter, JustDoWork, GoRouter, SeekAI, and KKtoken AI plus YAML declarative check-in runtime.
- **CLI Sync Engine**: One-click configuration merge and rollback for Claude Code, Codex, and Hermes.
- **Zero-Plaintext Security**: AES-256-GCM encrypted secret storage, Apple Keychain bridge, automatic header redaction, and SQLite audit logging.
- **Cross-Platform Delivery**: Native macOS menu bar application (`.dmg`) and multi-architecture Docker image.

## Quick Start

### 1. Build and Run Local Binary

```bash
go build -o bin/relayhub ./cmd/relayhub
./bin/relayhub -full-stack
```

Web Console is available at `http://127.0.0.1:8790`.

### 2. Run with Docker Compose

```bash
docker compose up -d
```

### 3. Run Tests & Validation

```bash
# Unit & integration tests
go test ./... -count=1

# Race detection
go test -race ./... -count=1

# Smoke test
./scripts/smoke-local.sh
```

## Documentation

- [Architecture & Design](docs/architecture.md)
- [Authoring Custom Providers](docs/provider-authoring.md)
- [CLI Synchronization Guide](docs/cli-sync.md)
- [Docker Deployment](docs/deployment-docker.md)
- [Security & Privacy](docs/security.md)
- [Full Specification](docs/superpowers/specs/2026-09-11-relayhub-design.md)

## License

AGPL-3.0-or-later.
