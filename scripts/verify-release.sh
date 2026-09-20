#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_PATH="${1:-$ROOT_DIR/bin/relayhub}"

if [ ! -f "$BIN_PATH" ]; then
    echo "==> Building fresh binary for release verification..."
    mkdir -p "$ROOT_DIR/bin"
    go build -o "$BIN_PATH" "$ROOT_DIR/cmd/relayhub"
fi

echo "==> Verifying binary execution & version..."
VERSION_OUT=$("$BIN_PATH" -version)
echo "$VERSION_OUT"

if [ -z "$VERSION_OUT" ]; then
    echo "ERROR: Empty version output"
    exit 1
fi

echo "==> Calculating SHA256 checksum..."
if command -v sha256sum &>/dev/null; then
    sha256sum "$BIN_PATH"
elif command -v shasum &>/dev/null; then
    shasum -a 256 "$BIN_PATH"
fi

echo "==> Scanning binary for accidental leak of development secret patterns..."
STRINGS_OUT=$(strings "$BIN_PATH" 2>/dev/null || true)
SECRET_REGEXES=(
    'sk-[a-zA-Z0-9]{20,}'
    'ghp_[a-zA-Z0-9]{20,}'
    'gho_[a-zA-Z0-9]{20,}'
    'AKIA[0-9A-Z]{16}'
    '-----BEGIN [A-Z ]*PRIVATE KEY-----'
    'xoxb-[0-9]{10,}-[0-9]{10,}-[a-zA-Z0-9]{24,}'
    'HERMES_CUSTOM_AXONHUB_API_KEY=ah-'
    'top-secret-password-12345'
)

for PATTERN in "${SECRET_REGEXES[@]}"; do
    MATCH=$(echo "$STRINGS_OUT" | grep -E -e "$PATTERN" | head -n 3 || true)
    if [ -n "$MATCH" ]; then
        echo "ERROR: Found sensitive pattern match for regex '$PATTERN':"
        echo "$MATCH"
        exit 1
    fi
done

# Step 2: Validate Docker and macOS builds report matching Core version if artifacts exist
echo "==> Validating Docker & macOS build version consistency..."
if command -v docker &>/dev/null; then
    if docker image inspect relayhub:test &>/dev/null; then
        DOCKER_VER=$(docker run --rm relayhub:test -version 2>/dev/null || true)
        if [ -n "$DOCKER_VER" ] && [ "$DOCKER_VER" != "$VERSION_OUT" ]; then
            echo "ERROR: Docker build version ($DOCKER_VER) does not match binary version ($VERSION_OUT)"
            exit 1
        fi
        echo "Docker image version matched: $DOCKER_VER"
    fi
fi

if [ -f "$ROOT_DIR/dist/RelayHub-macOS-arm64.dmg" ] || [ -f "$ROOT_DIR/dist/RelayHub-macOS-apple-silicon.dmg" ] || [ -f "$ROOT_DIR/dist/RelayHub-macOS-intel.dmg" ] || [ -d "$ROOT_DIR/dist/RelayHub-amd64.app" ]; then
    MACOS_BIN=""
    if [ -f "$ROOT_DIR/dist/RelayHub-amd64.app/Contents/MacOS/RelayHub" ]; then
        MACOS_BIN="$ROOT_DIR/dist/RelayHub-amd64.app/Contents/MacOS/RelayHub"
    elif [ -f "$ROOT_DIR/dist/RelayHub-amd64.app/Contents/MacOS/relayhub" ]; then
        MACOS_BIN="$ROOT_DIR/dist/RelayHub-amd64.app/Contents/MacOS/relayhub"
    fi

    if [ -n "$MACOS_BIN" ] && [ -x "$MACOS_BIN" ]; then
        MACOS_VER=$("$MACOS_BIN" -version 2>/dev/null || true)
        if [ -n "$MACOS_VER" ] && [ "$MACOS_VER" != "$VERSION_OUT" ]; then
            echo "ERROR: macOS app version ($MACOS_VER) does not match binary version ($VERSION_OUT)"
            exit 1
        fi
        echo "macOS app version matched: $MACOS_VER"
    fi
fi

echo "==> Release verification passed successfully!"
