#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TEMP_DIR=$(mktemp -d /tmp/relayhub-smoke-XXXXXX)
BIN_PATH="$TEMP_DIR/relayhub"

cleanup() {
  if [ -n "${CORE_PID:-}" ]; then
    kill "$CORE_PID" 2>/dev/null || true
    wait "$CORE_PID" 2>/dev/null || true
  fi
  if [ -n "${MOCK_PID:-}" ]; then
    kill "$MOCK_PID" 2>/dev/null || true
    wait "$MOCK_PID" 2>/dev/null || true
  fi
  rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

echo "==> Building relayhub binary..."
go build -o "$BIN_PATH" ./cmd/relayhub

echo "==> Checking binary version output..."
"$BIN_PATH" -version

echo "==> Setting up local test environment..."
DATA_DIR="$TEMP_DIR/data"
mkdir -p "$DATA_DIR"

# Allocate dynamic unused ports
MOCK_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')

echo "==> Starting mock upstream server on port $MOCK_PORT..."
cat << 'PYEOF' > "$TEMP_DIR/mock_upstream.py"
import http.server
import json
import sys

port = int(sys.argv[1])

class MockHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def do_GET(self):
        if self.path == "/direct-test":
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"smoke-mock-ok")
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        if self.path == "/v1/chat/completions":
            auth = self.headers.get("Authorization", "")
            if auth != "Bearer smoke-upstream-secret":
                self.send_response(401)
                self.end_headers()
                self.wfile.write(b'{"error":"unauthorized"}')
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"id":"chatcmpl-smoke","choices":[{"message":{"role":"assistant","content":"smoke-gateway-pong"}}]}')
        else:
            self.send_response(404)
            self.end_headers()

server = http.server.ThreadingHTTPServer(("127.0.0.1", port), MockHandler)
server.serve_forever()
PYEOF

python3 "$TEMP_DIR/mock_upstream.py" "$MOCK_PORT" &
MOCK_PID=$!

# Wait for mock upstream ready
for i in $(seq 1 30); do
    if curl -s "http://127.0.0.1:$MOCK_PORT/direct-test" | grep -q "smoke-mock-ok"; then
        break
    fi
    sleep 0.1
done

echo "==> Starting RelayHub Core..."
"$BIN_PATH" -full-stack -data-dir "$DATA_DIR" &
CORE_PID=$!

# Wait for Core /healthz
echo "==> Probing RelayHub healthz..."
API_READY=0
for i in $(seq 1 50); do
    if curl -s "http://127.0.0.1:8790/healthz" | grep -q '"status":"ok"'; then
        API_READY=1
        break
    fi
    sleep 0.1
done

if [ "$API_READY" -ne 1 ]; then
    echo "ERROR: RelayHub management API failed to become healthy"
    exit 1
fi
echo "==> Core health check passed!"

echo "==> Testing HTTP proxy..."
HTTP_RESP=$(curl -s -x "http://127.0.0.1:8787" "http://127.0.0.1:$MOCK_PORT/direct-test")
if [ "$HTTP_RESP" != "smoke-mock-ok" ]; then
    echo "ERROR: Unexpected HTTP proxy response: $HTTP_RESP"
    exit 1
fi
echo "==> HTTP proxy OK!"

echo "==> Testing SOCKS5 proxy..."
SOCKS_RESP=$(curl -s --socks5 "127.0.0.1:8788" "http://127.0.0.1:$MOCK_PORT/direct-test")
if [ "$SOCKS_RESP" != "smoke-mock-ok" ]; then
    echo "ERROR: Unexpected SOCKS5 proxy response: $SOCKS_RESP"
    exit 1
fi
echo "==> SOCKS5 proxy OK!"

echo "==> Configuring Gateway routing via Management API..."
curl -fsS -X POST "http://127.0.0.1:8790/api/v1/secrets" \
    -H "Content-Type: application/json" \
    -d '{"ref":"cred-smoke-key","value":"smoke-upstream-secret"}' >/dev/null

curl -fsS -X POST "http://127.0.0.1:8790/api/v1/providers" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"p-smoke\",\"name\":\"SmokeProvider\",\"adapter_type\":\"generic\",\"protocol\":\"openai-chat\",\"base_url_template\":\"http://127.0.0.1:$MOCK_PORT\",\"enabled\":true}" >/dev/null

curl -fsS -X POST "http://127.0.0.1:8790/api/v1/channels" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"c-smoke\",\"provider_id\":\"p-smoke\",\"name\":\"SmokeChan\",\"base_url\":\"http://127.0.0.1:$MOCK_PORT\",\"credential_ref\":\"cred-smoke-key\",\"enabled\":true,\"routing_enabled\":true}" >/dev/null

curl -fsS -X POST "http://127.0.0.1:8790/api/v1/models" \
    -H "Content-Type: application/json" \
    -d '{"id":"smoke-model","display_name":"SmokeModel","enabled":true}' >/dev/null

curl -fsS -X POST "http://127.0.0.1:8790/api/v1/provider-models" \
    -H "Content-Type: application/json" \
    -d '{"id":"pm-smoke","provider_id":"p-smoke","channel_id":"c-smoke","model_id":"smoke-model","upstream_model_name":"gpt-smoke","protocol":"openai-chat","enabled":true}' >/dev/null

echo "==> Issuing local API key..."
KEY_RESP=$(curl -fsS -X POST "http://127.0.0.1:8790/api/v1/keys" -H "Content-Type: application/json" -d '{}')
API_KEY=$(echo "$KEY_RESP" | python3 -c 'import sys, json; print(json.load(sys.stdin)["key"])')

if [ -z "$API_KEY" ]; then
    echo "ERROR: Failed to issue local API key"
    exit 1
fi
echo "==> Issued local API key successfully!"

echo "==> Testing Gateway chat completions..."
GW_RESP=$(curl -fsS -X POST "http://127.0.0.1:8789/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"smoke-model","messages":[{"role":"user","content":"smoke-ping"}]}')

if ! echo "$GW_RESP" | grep -q "smoke-gateway-pong"; then
    echo "ERROR: Gateway response missing expected content: $GW_RESP"
    exit 1
fi
echo "==> Gateway routing OK!"

echo "==> RelayHub live smoke test passed successfully!"
