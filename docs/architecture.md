# RelayHub Architecture

RelayHub is a local-first AI gateway, transparent proxy, and check-in aggregator designed to unify access to multiple upstream providers.

```text
┌────────────────────────────────────────────────────────────┐
│                    Local Control Plane                     │
│     Desktop shell (icon rail + inspector) / macOS menu bar │
└─────────────────────────────┬──────────────────────────────┘
                              │
┌─────────────────────────────▼──────────────────────────────┐
│                    RelayHub Go Core                        │
│                                                            │
│  [1. Transparent Proxy]    [2. AI Gateway]                │
│   HTTP/HTTPS (8787)         OpenAI / Anthropic (8789)      │
│   SOCKS5 (8788)             Streaming / Failover           │
│                                                            │
│  [3. Resource Registry & Router]                           │
│   Provider -> Channel -> Model -> ProviderModel -> Route  │
│   Deterministic Selection & Circuit Breakers               │
│                                                            │
│  [4. Check-in Scheduler]   [5. CLI Sync Engine]           │
│   5 Builtin + Declarative   Codex / Claude Code / Hermes   │
│                                                            │
│  [6. Secure Storage & Secrets Engine]                      │
│   AES-256-GCM, Argon2id, SQLite, System Keychain Bridge    │
└────────────────────────────────────────────────────────────┘
```

## Six-Layer Resource Decoupling

1. **Provider**: Upstream type and protocol specification (e.g. OpenAI, Anthropic, AgentRouter).
2. **Channel**: Physical connection endpoint carrying Base URL, quota state, health, and credentials.
3. **Model**: Logical unified model identity (e.g. `claude-3-7-sonnet`, `coding`).
4. **ProviderModel**: Explicit mapping from Logical Model to upstream model name per channel.
5. **ModelGroup**: User-defined routing groups (e.g. `coding`, `reasoning`).
6. **Route**: Matching rules routing requests deterministically by protocol and pattern.
