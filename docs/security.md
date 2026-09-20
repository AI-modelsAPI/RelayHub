# Security Architecture & Policies

## 1. Local-First Listening
By default, RelayHub binds all services exclusively to loopback addresses (`127.0.0.1`). Public exposure of management port `8790` is forbidden without explicit bearer token, CIDR allowlist, and rate limiting.

## 2. Secrets Encryption at Rest
All upstream API keys and cookies are encrypted using AES-256-GCM.
Keys are protected via:
- macOS: Apple Keychain Services (`Security.framework`)
- Docker/Linux: Local master key with `0600` permissions.

## 3. Redaction & Audit
- All HTTP headers (`Authorization`, `Cookie`, `Set-Cookie`, `x-api-key`) and API token shapes (`sk-...`, `ah-...`) are automatically masked prior to log emission.
- Sensitive client cookies are stripped before forwarding requests upstream.
- Database audit logs track mutating actions with actor IP, timestamp, and request ID.
