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

The desktop-paradigm console is served at `http://127.0.0.1:8790` during development (icon rail + inspector; not a website admin). See `docs/ui/desktop-paradigm.md`.

### 2. Run with Docker Compose

```bash
docker compose up -d
```

### 3. Run Tests & Validation

```bash
# Everything CI runs, on this machine: web asset contract + frontend tests,
# unit/integration tests with the race detector, live full-stack smoke over
# real sockets, and (on macOS) the packaged desktop app lifecycle suite.
make test-real-device

# Pieces
make check-web test-web          # web/ == internal/web/, frontend<->API route contract
go test -race ./... -count=1     # unit, integration, e2e (real Chrome CDP test runs if Chrome is installed)
./scripts/smoke-local.sh         # proxies + gateway on the real ports 8787-8790
make test-macos                  # build both arches, verify, run the .app lifecycle tests
make vuln                        # govulncheck
```

Real-device audit and evidence: `docs/audit/REAL-DEVICE-AUDIT-2026-09-22.md`.

## Documentation

- [Architecture](docs/architecture.md)
- [Desktop UI paradigm](docs/ui/desktop-paradigm.md)
- [Development plan](docs/dev/PLAN-2026-09-20.md)
- [Differentiation proposals](docs/proposals/2026-09-20-differentiation.md)
- [Authoring Custom Providers](docs/provider-authoring.md)
- [CLI Synchronization Guide](docs/cli-sync.md)
- [Docker Deployment](docs/deployment-docker.md)
- [Security & Privacy](docs/security.md)
- [Full Specification](docs/superpowers/specs/2026-09-11-relayhub-design.md)

## License

AGPL-3.0-or-later.
