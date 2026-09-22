# RelayHub Architecture

RelayHub is a **local AI egress daemon**: a local-first AI gateway, transparent proxy, and check-in aggregator designed so that keys never leave the machine, every outbound request takes one explainable exit, and free relay quota is kept alive and turned into a routing signal.

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

## Outbound Paths Share One Egress Policy

Three components talk to the outside world; all of them ask `internal/egress` for their client so the same account is never seen from two networks:

| Path | Package | How the exit is applied |
|---|---|---|
| Gateway forwarding | `internal/gateway` (`HTTPUpstream.ClientFor`) | per-channel `http.Client` (streaming, no overall timeout; the request context bounds it) |
| Check-in / balance / model discovery | `internal/adapter` (`ClientProvider`) | per-channel `http.Client` with the operation's timeout |
| Browser (CDP) check-in | `internal/browser` (`--proxy-server`) | Chromium flag; credentialed proxies refuse to launch (fail closed) |

Resolution order: `Channel.proxy_url` → `egress_proxy_url` (config / `RELAYHUB_EGRESS_PROXY`) → process environment → direct. `direct` is an explicit opt-out. Invalid proxies fail closed rather than falling back to another exit.

## Health and Quota Feedback Loop

```text
gateway attempt ──► health.Registry ──► router (latency / success-rate / quota-first)
   TTFB, status,        sliding window,          │
   Retry-After          breaker + backoff        ▼
                             │            Decision.Excluded (per-candidate reason)
check-in / hourly poll ──► SetQuota (USD) ──────┘
                             │
                             ├──► health_records (transitions only)
                             └──► notify (Webhook / Bark / Telegram, cooldown)
```

- Latency is measured from *before* the upstream request (time to first byte); success rate is a sliding window of the last 50 outcomes.
- `429` responses honour `Retry-After`; breaker cooldown grows exponentially with consecutive trips.
- Balances are refreshed after every check-in and hourly, normalised to USD, stored as a structured `QuotaSnapshot` on the channel, and pushed into the registry so `quota-first` can prefer channels that still have credit.
- Browser check-ins are verified server-side (`adapter.CheckinVerifier`) before a success is recorded.
- Only state transitions are persisted and notified; per-request outcomes stay in memory and `request_records`.

