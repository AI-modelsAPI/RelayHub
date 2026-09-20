# Provider Authoring Guide

RelayHub supports two mechanisms for adding third-party providers without modifying Core Go code.

## 1. Generic Providers (Zero-Code)

For standard OpenAI, Anthropic, or Gemini compatible relays:
1. Open the Web Console (`http://127.0.0.1:8790`).
2. Navigate to **Providers** -> **Add Provider**.
3. Fill in:
   - `Name`: e.g. `My Custom Relay`
   - `Protocol`: `openai` or `anthropic`
   - `AdapterType`: `generic`
4. Add a **Channel** under this Provider with its Base URL and API Key.
5. Map models under **Provider Models**.

## 2. Declarative Check-in Adapters (YAML/JSON)

For custom daily check-ins:
```yaml
name: custom-relay
checkin:
  method: POST
  path: /api/v1/user/checkin
  headers:
    Content-Type: application/json
  body: '{"action":"sign"}'
balance:
  method: GET
  path: /api/v1/user/quota
timeout: 10s
```
Security limits enforced by Core:
- Method allowlist: Only `GET` and `POST` permitted.
- Path traversal rejection: `..` and absolute URLs (`http://`) forbidden.
- Maximum response size: 2MB limit to prevent memory bloat.
