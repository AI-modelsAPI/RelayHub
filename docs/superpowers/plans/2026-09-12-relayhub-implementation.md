# RelayHub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local-first Go AI gateway that aggregates multiple providers and channels, routes independent logical models and model groups, performs authorized site check-ins, exposes transparent HTTP/SOCKS5 proxying, synchronizes Codex/Claude Code/Hermes configuration, and ships through macOS and Docker artifacts.

**Architecture:** A shared Go Core owns SQLite persistence, encrypted credentials, Provider/Channel/Model/ProviderModel/ModelGroup/Route resources, routing, protocol gateways, transparent proxies, check-in scheduling, CLI configuration sync, diagnostics, and versioned import/export. A local Web UI calls the Core management API. The macOS shell manages the Core process, Keychain integration, notifications, and packaging; Docker packages the same Core and UI with `/data` persistence. No realtime cloud synchronization is implemented.

**Tech Stack:** Go (current supported toolchain pinned in `go.mod`), SQLite, `net/http`, `net`, `context`, TOML/JSON/YAML parsers with pinned versions, embedded Web UI assets, macOS desktop shell, Docker Buildx, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-11-relayhub-design.md`

## Global Constraints

- Default listeners bind only to `127.0.0.1`: HTTP proxy `8787`, SOCKS5 `8788`, AI Gateway `8789`, management UI/API `8790`.
- Provider, Channel, Model, ProviderModel, ModelGroup, and Route remain separate resources; a provider must never own the logical model registry directly.
- Transparent proxying, AI Gateway routing, and check-in scheduling are separate services; a provider adapter failure must not prevent the Core from starting.
- Secrets are encrypted at rest; plaintext fallback is forbidden when the configured key backend is unavailable.
- Logs and diagnostics must not contain complete API keys, cookies, authorization headers, OAuth code/state, passwords, TOTP values, prompts, request bodies, or response bodies.
- CLI configuration changes are previewed, backed up, atomically written, read back, and health-checked; parse failure never overwrites the source file.
- Check-in code operates only on user-authorized accounts and never bypasses CAPTCHA, Turnstile, device verification, or access controls.
- A streamed request cannot fail over after its first valid response event has been sent.
- No realtime cloud sync, public account pool, billing, batch registration, or public-open-proxy behavior is implemented.
- Each task must end with focused tests; run `gofmt`, `go vet`, targeted tests, and the relevant integration/build check before marking it complete.

---

## Task 1: Bootstrap the Go Core and repository layout

**Files:**
- Create: `go.mod`
- Create: `cmd/relayhub/main.go`
- Create: `internal/app/app.go`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/app/app_test.go`
- Create: `Makefile`
- Create: `.gitignore`
- Create: `README.md`

**Interfaces:**
- Produces `app.New(config Config) (*App, error)`, `App.Start(ctx) error`, and `App.Shutdown(ctx) error`.
- Produces a single `relayhub` executable that can start in foreground mode and reports its version/build information.

- [ ] **Step 1: Write the failing bootstrap tests**

Create tests that construct an app with a temporary data directory, assert that startup creates no listeners until `Start` is called, assert that `Start` followed by `Shutdown` returns cleanly, and assert that the version endpoint data is populated from build defaults.

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./internal/app ./internal/buildinfo -count=1`

Expected: FAIL because the module and app lifecycle are not implemented.

- [ ] **Step 3: Implement the minimal module and lifecycle**

Add the module, a dependency-free lifecycle object, signal-aware `main`, build metadata variables, and a Makefile with `format`, `test`, `vet`, `build`, and `smoke` targets. Do not add networking or database behavior in this task.

- [ ] **Step 4: Run formatting, tests, and vet**

Run: `gofmt -w cmd internal && go test ./internal/app ./internal/buildinfo -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd/relayhub internal/app internal/buildinfo internal/app/app_test.go Makefile .gitignore README.md
git commit -m "chore: bootstrap relayhub core"
```

## Task 2: Configuration, data directories, SQLite migrations, and repository interfaces

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/storage/db.go`
- Create: `internal/storage/migrations.go`
- Create: `internal/storage/migrations/001_initial.sql`
- Create: `internal/storage/migrations_test.go`
- Create: `internal/domain/provider.go`
- Create: `internal/domain/channel.go`
- Create: `internal/domain/model.go`
- Create: `internal/domain/route.go`
- Create: `internal/repository/repository.go`
- Create: `internal/repository/sqlite.go`
- Create: `internal/repository/sqlite_test.go`

**Interfaces:**
- `config.Load(path string) (config.Config, error)` resolves defaults without secrets.
- `storage.Open(path string) (*storage.DB, error)` and `DB.Migrate(ctx) error` are idempotent.
- Repository methods use context and return domain objects for Provider, Channel, Model, ProviderModel, ModelGroup, Route, and status records.

- [ ] **Step 1: Write migration and repository tests first**

Test that a temporary database migrates exactly once, a second migration is a no-op, all six resource families can be inserted and read back, and a failed migration leaves the previous schema usable.

- [ ] **Step 2: Run the tests to verify the missing implementation fails**

Run: `go test ./internal/config ./internal/storage ./internal/repository -count=1`

Expected: FAIL with missing packages, symbols, and schema.

- [ ] **Step 3: Implement configuration and schema**

Implement platform-neutral data directory resolution, explicit listener defaults, migration bookkeeping, and the tables required by the spec: `providers`, `channels`, `credentials`, `models`, `provider_models`, `model_groups`, `model_group_members`, `routes`, `checkin_records`, `health_records`, `request_records`, `cli_sync_records`, `config_backups`, and `schema_migrations`. Add foreign keys, unique constraints, indexes for route lookup and health state, and timestamps in UTC.

- [ ] **Step 4: Implement repository CRUD with transactional writes**

Implement focused repositories and transaction helpers. Ensure Provider deletion is rejected while referenced Channels exist unless an explicit cascade operation is later added; preserve logical model IDs when ProviderModel mappings change.

- [ ] **Step 5: Run validation**

Run: `gofmt -w internal && go test ./internal/config ./internal/storage ./internal/repository -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config internal/storage internal/domain internal/repository
git commit -m "feat: add persistent resource model"
```

## Task 3: Secret storage, local authentication, redaction, and audit logging

**Files:**
- Create: `internal/secrets/store.go`
- Create: `internal/secrets/keyring.go`
- Create: `internal/secrets/store_test.go`
- Create: `internal/logging/logger.go`
- Create: `internal/logging/redact.go`
- Create: `internal/logging/redact_test.go`
- Create: `internal/audit/audit.go`
- Create: `internal/auth/local_keys.go`
- Create: `internal/auth/local_keys_test.go`

**Interfaces:**
- `secrets.Store.Put(ctx, ref, plaintext) error`, `Get(ctx, ref) ([]byte, error)`, and `Delete(ctx, ref) error`.
- `logging.Redactor` masks known secret patterns and sensitive header values.
- `auth.LocalKeyService` creates, revokes, and validates RelayHub local API keys without returning stored secrets.

- [ ] **Step 1: Write tests for encryption, redaction, and key lifecycle**

Test round-trip encryption, wrong-key rejection, tamper rejection, deletion, key creation/revocation, and redaction of Authorization, Cookie, API-key-shaped strings, OAuth parameters, and secret values embedded in error text. Assert that ordinary model names and URLs remain readable.

- [ ] **Step 2: Run focused tests and verify they fail**

Run: `go test ./internal/secrets ./internal/logging ./internal/auth -count=1`

Expected: FAIL before implementations exist.

- [ ] **Step 3: Implement encrypted storage and platform key providers**

Use authenticated encryption with a per-installation data-encryption key. Define a key-provider interface; implement a file-based provider for Docker and a platform hook for macOS Keychain. Refuse startup of secret-dependent operations when no key provider is available; never write plaintext secrets to SQLite.

- [ ] **Step 4: Implement redaction and audit events**

Use structured fields rather than interpolated strings. Redact before sink emission, add request/task IDs, and persist only permitted audit metadata. Ensure diagnostics can never recover the original secret from the redacted event.

- [ ] **Step 5: Run validation**

Run: `gofmt -w internal && go test ./internal/secrets ./internal/logging ./internal/auth -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/secrets internal/logging internal/audit internal/auth
git commit -m "feat: protect secrets and redact diagnostics"
```

## Task 4: Provider, Channel, Model, ModelGroup, and Route services

**Files:**
- Create: `internal/catalog/service.go`
- Create: `internal/catalog/service_test.go`
- Create: `internal/router/matcher.go`
- Create: `internal/router/matcher_test.go`
- Create: `internal/router/selector.go`
- Create: `internal/router/selector_test.go`
- Create: `internal/health/state.go`
- Create: `internal/health/state_test.go`

**Interfaces:**
- `catalog.Service` validates and persists the six resource types.
- `router.Resolve(ctx, Request) (Decision, error)` returns the selected logical Model, ProviderModel, Channel, transform, and exclusion reasons.
- `health.Registry` tracks health, cooldown, quota, authentication, and circuit state.

- [ ] **Step 1: Write red tests for resource validation and deterministic routing**

Cover duplicate IDs, missing foreign keys, exact-model precedence over group and wildcard routes, disabled members, provider/channel mismatches, weighted selection with a deterministic test source, cooldown filtering, quota exhaustion, and no-candidate errors that include exclusion reasons.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/catalog ./internal/router ./internal/health -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement catalog validation and route matching**

Implement explicit route precedence, model-group membership, ProviderModel lookup, protocol/capability filtering, and stable candidate ordering. Keep resource CRUD independent from runtime health state.

- [ ] **Step 4: Implement health and selection policies**

Implement priority, weight, fixed-channel, round-robin, latency, success-rate, and quota-aware policies. Add circuit transitions and Retry-After cooldown state. Make selection decisions immutable for the lifetime of a request.

- [ ] **Step 5: Run validation**

Run: `gofmt -w internal && go test ./internal/catalog ./internal/router ./internal/health -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog internal/router internal/health
git commit -m "feat: separate models providers and routing"
```

## Task 5: HTTP management API and minimal Web UI

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/errors.go`
- Create: `internal/api/server_test.go`
- Create: `web/index.html`
- Create: `web/app.js`
- Create: `web/styles.css`
- Create: `internal/web/embed.go`

**Interfaces:**
- Versioned management endpoints under `/api/v1` for health, providers, channels, models, model groups, routes, check-in status, logs, and settings.
- `GET /healthz` is unauthenticated and local-only; mutating management endpoints require a local management token when configured.
- Embedded UI displays resource lists, health, route decisions, and a safe configuration form without exposing secrets.

- [ ] **Step 1: Write handler tests**

Test health, list/create/update validation, malformed JSON, authentication failure, masked secret fields, deterministic error envelopes, and request ID propagation.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/api ./internal/web -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement the API and embed the UI**

Use JSON schemas enforced at the service boundary, return HTTP status codes by error class, and keep secrets write-only. Add a compact responsive UI with top-level sections: Overview, Providers, Channels/Accounts, Models, Model Groups, Routes, Check-in, Requests/Usage, CLI Sync, Import/Export, Settings.

- [ ] **Step 4: Run validation**

Run: `gofmt -w internal && go test ./internal/api ./internal/web -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/web web
 git commit -m "feat: add local management API and web console"
```

## Task 6: Transparent HTTP CONNECT and SOCKS5 proxies

**Files:**
- Create: `internal/proxy/http_proxy.go`
- Create: `internal/proxy/socks5_proxy.go`
- Create: `internal/proxy/proxy.go`
- Create: `internal/proxy/proxy_test.go`
- Create: `internal/proxy/testserver_test.go`

**Interfaces:**
- `proxy.Server.Start(ctx) error`, `Shutdown(ctx) error`, and `Addr() net.Addr`.
- HTTP proxy supports ordinary HTTP requests and CONNECT tunnels.
- SOCKS5 supports TCP, IPv4, IPv6, optional username/password authentication, cancellation, and timeouts.

- [ ] **Step 1: Write integration tests against local upstream servers**

Test HTTP-to-HTTP, HTTP CONNECT-to-HTTPS, SOCKS5-to-HTTP/HTTPS, IPv4, IPv6 where available, large bodies, long-lived streaming, timeout, client cancellation, and authentication. Assert no request body is written to diagnostic logs.

- [ ] **Step 2: Run proxy tests and verify they fail**

Run: `go test ./internal/proxy -count=1 -timeout=2m`

Expected: FAIL.

- [ ] **Step 3: Implement the proxy servers**

Use bounded connection handling, context cancellation, idle timeouts, maximum header/body limits for non-tunnel HTTP, CONNECT authority validation, optional proxy authentication, and structured redacted metadata logs. Do not route transparent proxy traffic through the AI router.

- [ ] **Step 4: Run validation and manual smoke test**

Run: `gofmt -w internal && go test ./internal/proxy -count=1 -timeout=2m && go vet ./...`

Then run the local smoke target against an ephemeral upstream and verify both HTTP and SOCKS5 paths.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy
git commit -m "feat: add local transparent proxies"
```

## Task 7: OpenAI and Anthropic Gateway with streaming and failover

**Files:**
- Create: `internal/gateway/gateway.go`
- Create: `internal/gateway/openai.go`
- Create: `internal/gateway/anthropic.go`
- Create: `internal/gateway/transform.go`
- Create: `internal/gateway/gateway_test.go`
- Create: `internal/gateway/sse_test.go`
- Create: `internal/usage/recorder.go`

**Interfaces:**
- `gateway.Handler` serves OpenAI Chat/Responses/Embeddings and Anthropic Messages endpoints.
- `gateway.Upstream` performs protocol-specific requests using a resolved immutable route decision.
- `usage.Recorder.Record(ctx, RequestRecord) error` stores metadata only.

- [ ] **Step 1: Write failing protocol tests**

Use local fake upstreams to test OpenAI non-streaming, OpenAI SSE, Anthropic non-streaming, Anthropic SSE, model mapping, tool calls, images, cancellation, 401 refresh retry, 429/5xx failover, malformed upstream responses, and the invariant that no failover happens after the first valid stream event.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/gateway ./internal/usage -count=1 -timeout=3m`

Expected: FAIL.

- [ ] **Step 3: Implement protocol handlers and transforms**

Implement request validation, local API-key authentication, route resolution, OpenAI/Anthropic request and response transformation, SSE flushing, usage extraction, client cancellation propagation, bounded retries, and consistent error envelopes. Keep transform functions pure and unit-testable.

- [ ] **Step 4: Implement failover and health updates**

Update health state only for classified upstream failures. Retry idempotent inference requests before the first response event; never replay an already-started stream. Do not retry client errors or non-idempotent check-in operations here.

- [ ] **Step 5: Run validation**

Run: `gofmt -w internal && go test ./internal/gateway ./internal/usage -count=1 -timeout=3m && go vet ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway internal/usage
git commit -m "feat: add unified ai gateways"
```

## Task 8: Generic Provider and declarative adapter runtime

**Files:**
- Create: `internal/adapter/adapter.go`
- Create: `internal/adapter/registry.go`
- Create: `internal/adapter/generic.go`
- Create: `internal/adapter/declarative.go`
- Create: `internal/adapter/declarative_test.go`
- Create: `internal/adapter/fixtures/example-provider.yaml`

**Interfaces:**
- `adapter.Registry.Register(name string, adapter ProviderAdapter) error` and `Resolve(provider Provider) (ProviderAdapter, error)`.
- Generic adapters support OpenAI Chat/Responses, Anthropic Messages, Gemini-compatible metadata, and no check-in behavior.
- Declarative adapters support only allowlisted methods, paths, headers, JSON extraction, success predicates, timeout, and rate limits.

- [ ] **Step 1: Write safety and behavior tests**

Test generic model discovery and health checks; declarative check-in/balance extraction; rejected unsupported methods, command execution fields, path traversal, dynamic expressions, excessive body sizes, and requests outside the declared base URL.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/adapter -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement the registry and generic adapter**

Register adapters independently from startup. A malformed provider definition returns a provider-local error. Generic adapters must never infer or invoke check-in endpoints.

- [ ] **Step 4: Implement the bounded declarative runtime**

Parse and validate the declarative schema, enforce URL and method allowlists, apply common redaction/rate limits/timeouts, and return typed results. Do not implement arbitrary script execution in this phase.

- [ ] **Step 5: Run validation**

Run: `gofmt -w internal && go test ./internal/adapter -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter
 git commit -m "feat: add generic and declarative providers"
```

## Task 9: Five built-in site adapters and check-in scheduler

**Files:**
- Create: `internal/adapter/agentrouter/adapter.go`
- Create: `internal/adapter/justdowork/adapter.go`
- Create: `internal/adapter/gorouter/adapter.go`
- Create: `internal/adapter/seekai/adapter.go`
- Create: `internal/adapter/kktoken/adapter.go`
- Create: `internal/checkin/scheduler.go`
- Create: `internal/checkin/state.go`
- Create: `internal/checkin/scheduler_test.go`
- Create: `internal/adapter/fixtures/*_responses.json`

**Interfaces:**
- Each adapter implements the common `ProviderAdapter` contract and only calls verified, documented/observed endpoints for that site.
- `checkin.Scheduler.Start(ctx)`, `RunNow(ctx, channelID)`, and `Stop(ctx)` coordinate per-channel jobs.
- Adapter fixtures contain no live credentials or secrets.

- [ ] **Step 1: Capture verified endpoint contracts before coding each adapter**

For each site, record endpoint, method, required headers, authentication lifecycle, idempotency behavior, success/already-done response, quota fields, 401/403/429/5xx behavior, and human-verification state in an adapter contract document. If an endpoint cannot be verified, leave that operation disabled rather than guessing.

- [ ] **Step 2: Write fixture-driven tests first**

Test validation, refresh, check-in success/already-completed, balance, models where supported, missing fields, authentication failure, rate limiting, upstream errors, and verification-required responses for all five adapters.

- [ ] **Step 3: Run adapter tests and verify they fail**

Run: `go test ./internal/adapter/... ./internal/checkin -count=1 -timeout=3m`

Expected: FAIL until adapters and scheduler are implemented.

- [ ] **Step 4: Implement adapters with isolated clients**

Implement only verified site behavior. Keep credentials in the secret store, use per-site request limits, classify errors, and make check-in idempotency explicit. Never open an OAuth or verification page from a background task.

- [ ] **Step 5: Implement scheduler and recovery**

Add daily/custom schedules, random delay, Retry-After handling, exponential backoff, per-channel mutexes, persisted task state, restart recovery, and notification events. A provider failure must not cancel unrelated tasks or the proxy/Gateway.

- [ ] **Step 6: Run validation**

Run: `gofmt -w internal && go test ./internal/adapter/... ./internal/checkin -count=1 -timeout=3m && go vet ./...`

Expected: PASS for all fixture tests.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter internal/checkin
git commit -m "feat: add check-in adapters and scheduler"
```

## Task 10: CLI configuration synchronization for Codex, Claude Code, and Hermes

**Files:**
- Create: `internal/clisync/engine.go`
- Create: `internal/clisync/backup.go`
- Create: `internal/clisync/diff.go`
- Create: `internal/clisync/engine_test.go`
- Create: `internal/clisync/codex/codex.go`
- Create: `internal/clisync/codex/codex_test.go`
- Create: `internal/clisync/claude/claude.go`
- Create: `internal/clisync/claude/claude_test.go`
- Create: `internal/clisync/hermes/hermes.go`
- Create: `internal/clisync/hermes/hermes_test.go`

**Interfaces:**
- `Syncer.Detect(ctx) (Target, error)`, `Preview(ctx, DesiredState) (Diff, error)`, `Apply(ctx, Diff) (Backup, error)`, and `Verify(ctx, DesiredState) error`.
- Codex targets `$CODEX_HOME/config.toml`; Claude targets `$CLAUDE_CONFIG_DIR/settings.json`; Hermes resolves `$HERMES_HOME` and profile paths.
- Secrets are represented by local RelayHub key references or environment variable names, never upstream secret values.

- [ ] **Step 1: Build parser-preservation fixtures and failing tests**

Test empty files, complex existing files, multiple providers, profiles, hooks, MCP, permissions, malformed files, environment-variable paths, duplicate execution, backup recovery, and preservation of fields outside the requested patch.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/clisync/... -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement common preview/backup/atomic-write engine**

Reject unsafe symlinks and malformed input, calculate an original hash, write a timestamped backup, apply an AST-level patch, fsync a temporary file, atomically replace it, read it back, and restore on post-write verification failure. Persist `cli_sync_records`.

- [ ] **Step 4: Implement Codex syncer**

Merge `model_providers.relayhub`, base URL `http://127.0.0.1:8789/v1`, wire API, model, and provider selection while retaining sandbox, approval, MCP, project, and other-provider configuration. Detect unsupported fields by the installed Codex schema/version and fail without writing.

- [ ] **Step 5: Implement Claude Code syncer**

Merge `ANTHROPIC_BASE_URL=http://127.0.0.1:8789`, local authentication reference, model-slot mappings, and optional profile data while retaining hooks, permissions, MCP, and unknown fields.

- [ ] **Step 6: Implement Hermes syncer**

Resolve the active `$HERMES_HOME`/profile, prefer official Hermes CLI configuration commands where available, place provider/model/base URL in `config.yaml`, place the local key reference in `.env`, and explicitly preview changes to default and delegation providers. Never modify skills, memory, sessions, gateway, or display settings unless selected.

- [ ] **Step 7: Run validation**

Run: `gofmt -w internal && go test ./internal/clisync/... -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/clisync
git commit -m "feat: synchronize cli configurations safely"
```

## Task 11: `.rhx` export/import, backup, migration, and recovery

**Files:**
- Create: `internal/export/export.go`
- Create: `internal/export/format.go`
- Create: `internal/export/crypto.go`
- Create: `internal/export/import.go`
- Create: `internal/export/export_test.go`
- Create: `internal/export/fixtures/`
- Modify: `internal/storage/migrations.go`

**Interfaces:**
- `export.Create(ctx, options) ([]byte, error)` creates non-sensitive or encrypted complete packages.
- `export.Preview(ctx, packageBytes) (Preview, error)` validates without mutating state.
- `export.Apply(ctx, packageBytes, conflictPolicy) error` imports atomically with rollback.

- [ ] **Step 1: Write tests for package format and conflict policy**

Test non-sensitive export excludes credentials, complete export decrypts only with the correct password, tampering is rejected, schema migration works, duplicate Provider/Channel conflicts are previewed, default policy does not overwrite existing records, and failed import restores the pre-import database.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/export ./internal/storage -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement manifest, checksums, and authenticated encryption**

Use a versioned manifest, Argon2id password derivation, AES-256-GCM authenticated encryption, explicit content types, and checksums. Keep secrets in a separate encrypted section and never include the export password.

- [ ] **Step 4: Implement preview and transactional import**

Validate before mutation, show resource counts and conflicts, migrate old schema versions, import in a transaction, and restore from a verified backup on failure.

- [ ] **Step 5: Run validation**

Run: `gofmt -w internal && go test ./internal/export ./internal/storage -count=1 && go vet ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/export internal/storage
 git commit -m "feat: add encrypted relayhub exports"
```

## Task 12: Wire the Core services and implement end-to-end local operation

**Files:**
- Modify: `internal/app/app.go`
- Create: `internal/app/wiring.go`
- Create: `internal/app/wiring_test.go`
- Modify: `cmd/relayhub/main.go`
- Create: `tests/e2e/local_stack_test.go`
- Create: `scripts/smoke-local.sh`

**Interfaces:**
- App wiring creates storage, secrets, repositories, catalog, health, adapters, scheduler, proxies, Gateway, API, and UI with shared lifecycle context.
- Shutdown drains listeners, stops schedulers, closes database, and never leaves background goroutines running.

- [ ] **Step 1: Write the end-to-end failing test**

Start a temporary Core with ephemeral ports and fake upstreams, create a Provider/Channel/Model/ProviderModel/Route through the management API, call OpenAI and Anthropic endpoints, exercise a transparent proxy, and verify request metadata is persisted without request content.

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test ./tests/e2e -count=1 -timeout=5m`

Expected: FAIL because services are not wired.

- [ ] **Step 3: Implement dependency wiring and lifecycle**

Construct services in dependency order, isolate adapter startup errors, register listeners on configured addresses, expose `/healthz`, and implement graceful shutdown with bounded drain time.

- [ ] **Step 4: Implement the smoke command**

The script starts a Core with a temporary data directory and ports, probes health, tests HTTP and SOCKS5 against a local upstream, tests Gateway routing against a fake provider, and exits nonzero on any failure. It must not use real credentials or third-party sites.

- [ ] **Step 5: Run full validation**

Run: `gofmt -w cmd internal tests && go test ./... -count=1 -timeout=10m && go vet ./... && ./scripts/smoke-local.sh`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd internal tests scripts
git commit -m "feat: wire relayhub local runtime"
```

## Task 13: Docker image, Compose, health check, and persistent-data verification

**Files:**
- Create: `Dockerfile`
- Create: `docker/entrypoint.sh`
- Create: `docker/healthcheck.sh`
- Create: `docker-compose.yml`
- Create: `docker/README.md`
- Create: `tests/docker/smoke.sh`
- Create: `.dockerignore`

**Interfaces:**
- Image runs the same `relayhub` Core as local mode, stores all persistent state under `/data`, runs as non-root, and emits logs to stdout.
- Compose exposes proxy/Gateway ports explicitly and keeps management binding local by default.

- [ ] **Step 1: Write the container smoke test**

Test empty-volume startup, migration, health check, non-root UID, SQLite persistence across restart, graceful stop, and local management binding. Use a fake upstream only.

- [ ] **Step 2: Build the image and verify the smoke test initially fails**

Run: `docker build -t relayhub:test . && ./tests/docker/smoke.sh`

Expected: FAIL until the image and compose wiring exist.

- [ ] **Step 3: Implement multi-stage Docker build and entrypoint**

Compile a static or minimally dependent binary, copy only runtime assets, create a non-root user, initialize `/data`, run migrations, and preserve exit codes. Do not include credentials or development fixtures in the image.

- [ ] **Step 4: Implement Compose and health check**

Add explicit ports, `/data` volume, environment-variable configuration, health check, restart policy, and comments warning against public management exposure.

- [ ] **Step 5: Run validation**

Run: `docker build --platform linux/amd64 -t relayhub:test . && ./tests/docker/smoke.sh`

If arm64 hardware/emulation is available, also run `docker build --platform linux/arm64 -t relayhub:test-arm64 .`.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile docker docker-compose.yml tests/docker .dockerignore
git commit -m "feat: package relayhub for docker"
```

## Task 14: macOS Core wrapper, Keychain adapter, menu bar shell, and DMG packaging

**Files:**
- Create: `desktop/macos/README.md`
- Create: `desktop/macos/RelayHubApp/`
- Create: `desktop/macos/KeychainStore.swift`
- Create: `desktop/macos/LaunchAgent.plist`
- Create: `scripts/build-macos.sh`
- Create: `tests/macos/smoke.sh`

**Interfaces:**
- The macOS shell starts/stops the shared Core, opens the local Web UI, displays status, and forwards notifications.
- Keychain adapter implements the Core secret-provider interface without changing domain behavior.

- [ ] **Step 1: Write packaging and lifecycle checks**

Test that the app bundle contains the Core, starts it with a temporary data directory, reports health, stops cleanly, uses the expected architecture, and does not include credentials.

- [ ] **Step 2: Implement the thin shell and Keychain bridge**

Add menu bar status, open-console action, start/stop/restart, login-item option, notifications, architecture detection, and Keychain storage. Keep all business services in the Core.

- [ ] **Step 3: Implement Intel and Apple Silicon builds**

Build `RelayHub-macOS-intel.dmg` and `RelayHub-macOS-apple-silicon.dmg`; sign/notarize only when release credentials are explicitly configured. Development builds must remain runnable without production signing credentials.

- [ ] **Step 4: Run validation**

Run the macOS smoke test on each available architecture and verify `go test ./...`, Core startup, Keychain round-trip, CLI path detection, and UI access.

- [ ] **Step 5: Commit**

```bash
git add desktop/macos scripts/build-macos.sh tests/macos
git commit -m "feat: add macos desktop packaging"
```

## Task 15: CI, release integrity, documentation, and final acceptance

**Files:**
- Create: `.github/workflows/test.yml`
- Create: `.github/workflows/release.yml`
- Create: `.github/workflows/docker.yml`
- Create: `docs/architecture.md`
- Create: `docs/provider-authoring.md`
- Create: `docs/cli-sync.md`
- Create: `docs/deployment-docker.md`
- Create: `docs/security.md`
- Modify: `README.md`
- Create: `scripts/verify-release.sh`

**Interfaces:**
- CI runs formatting, unit/integration tests, vet, Docker smoke tests, and platform build checks.
- Release workflow publishes version-matched macOS and multi-architecture Docker artifacts with checksums.
- Documentation describes the stable resource model and safe extension boundaries.

- [ ] **Step 1: Write release verification tests/scripts**

Verify binary version consistency, checksums, image labels, absence of known secret patterns in release artifacts, schema/config compatibility, and that Docker and macOS builds report the same Core version.

- [ ] **Step 2: Implement CI gates**

Run `gofmt` check, `go test ./...`, `go vet ./...`, smoke tests, Docker builds, and static artifact checks. Do not publish on failed tests or missing checksums.

- [ ] **Step 3: Document extension and deployment contracts**

Document Provider vs Channel vs Model vs ProviderModel vs ModelGroup vs Route, generic/declarative provider setup, verified adapter requirements, CLI synchronization safety, Docker network exposure, backup/recovery, and security limits.

- [ ] **Step 4: Run the complete release gate**

Run:

```bash
gofmt -l .
go test ./... -count=1 -timeout=15m
go vet ./...
./scripts/smoke-local.sh
./scripts/verify-release.sh
```

Expected: no formatting output, all tests pass, vet passes, smoke passes, and release verification reports matching versions/checksums.

- [ ] **Step 5: Commit**

```bash
git add .github docs README.md scripts
 git commit -m "ci: add relayhub release gates and documentation"
```

## Final Acceptance Checklist

- [ ] A fresh local run starts with no public listeners and exposes `/healthz`.
- [ ] HTTP proxy supports HTTP and HTTPS CONNECT; SOCKS5 supports TCP and authentication.
- [ ] OpenAI Chat/Responses and Anthropic Messages support streaming, tools, cancellation, and typed errors.
- [ ] Provider, Channel, Model, ProviderModel, ModelGroup, and Route are independently manageable.
- [ ] One logical Model can map to multiple Provider/Channel upstream model names.
- [ ] Route selection is deterministic, health-aware, quota-aware, and records exclusion reasons.
- [ ] A streamed request never fails over after its first valid event.
- [ ] Generic compatible providers can be added without changing Core code.
- [ ] Five built-in adapters are fixture-tested and isolated from each other.
- [ ] Check-in scheduling is idempotent, rate-limited, restart-safe, and verification-safe.
- [ ] Credentials are encrypted and logs/diagnostics are redacted.
- [ ] Codex, Claude Code, and Hermes config sync previews, backs up, atomically writes, verifies, and preserves unrelated settings.
- [ ] `.rhx` non-sensitive and encrypted complete exports work with conflict preview and rollback.
- [ ] Docker runs non-root with persistent `/data`, health checks, and no default public management exposure.
- [ ] macOS Intel and Apple Silicon shells use the same Core and can use Keychain.
- [ ] CI validates the complete test/build/release path before publication.
