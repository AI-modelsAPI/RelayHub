#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_PATH="${1:-}"

# The reference binary is stamped exactly like scripts/build-macos.sh and the
# Makefile so version tokens are comparable across artifacts.
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-X relayhub/internal/buildinfo.version=$VERSION -X relayhub/internal/buildinfo.commit=$COMMIT -X relayhub/internal/buildinfo.date=$DATE"

# Without an explicit binary argument the reference is ALWAYS rebuilt: a stale
# bin/relayhub from an earlier, differently-stamped build made the parity check
# fail against a correct app (observed on the first real-device run).
if [ -z "$BIN_PATH" ]; then
    BIN_PATH="$ROOT_DIR/bin/relayhub"
    echo "==> Building fresh reference binary for release verification..."
    mkdir -p "$ROOT_DIR/bin"
    go build -ldflags "$LDFLAGS" -o "$BIN_PATH" "$ROOT_DIR/cmd/relayhub"
elif [ ! -x "$BIN_PATH" ]; then
    echo "ERROR: binary not found or not executable: $BIN_PATH"
    exit 1
fi

echo "==> Verifying binary execution & version..."
VERSION_OUT=$("$BIN_PATH" -version)
echo "$VERSION_OUT"

if [ -z "$VERSION_OUT" ]; then
    echo "ERROR: Empty version output"
    exit 1
fi

# "relayhub <version> (commit <c>, built <d>, goX os/arch)": compare the version
# token only. Build date and os/arch legitimately differ between a host binary
# and a cross-built artifact; comparing the whole line produced false failures
# (e.g. arm64 host vs amd64 app under Rosetta) and masked real ones behind
# `|| true` (AUDIT RH-33).
version_token() { printf '%s' "$1" | awk '{print $2}'; }
EXPECTED_TOKEN="$(version_token "$VERSION_OUT")"
if [ -z "$EXPECTED_TOKEN" ]; then
    echo "ERROR: could not parse version token from: $VERSION_OUT"
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
        if [ -z "$DOCKER_VER" ]; then
            echo "ERROR: relayhub:test image exists but '-version' produced no output"
            exit 1
        fi
        if [ "$(version_token "$DOCKER_VER")" != "$EXPECTED_TOKEN" ]; then
            echo "ERROR: Docker build version ($DOCKER_VER) does not match binary version ($VERSION_OUT)"
            exit 1
        fi
        echo "Docker image version matched: $DOCKER_VER"
    fi
fi

# Only the bundle matching the host CPU is executed: the other architecture
# would run under Rosetta (or not at all) and report a different os/arch.
case "$(uname -m)" in
    arm64)  APP_ARCH="arm64" ;;
    x86_64) APP_ARCH="amd64" ;;
    *)      APP_ARCH="" ;;
esac
MACOS_BIN=""
if [ -n "$APP_ARCH" ] && [ -d "$ROOT_DIR/dist/RelayHub-$APP_ARCH.app" ]; then
    MACOS_BIN="$ROOT_DIR/dist/RelayHub-$APP_ARCH.app/Contents/MacOS/RelayHub"
fi
if [ -n "$MACOS_BIN" ]; then
    if [ ! -x "$MACOS_BIN" ]; then
        echo "ERROR: macOS bundle present but shell executable missing: $MACOS_BIN"
        exit 1
    fi
    if ! MACOS_VER=$("$MACOS_BIN" -version 2>&1); then
        echo "ERROR: macOS app -version failed: $MACOS_VER"
        exit 1
    fi
    if [ "$(version_token "$MACOS_VER")" != "$EXPECTED_TOKEN" ]; then
        echo "ERROR: macOS app version ($MACOS_VER) does not match binary version ($VERSION_OUT)"
        exit 1
    fi
    echo "macOS app version matched: $MACOS_VER"
    CORE_BIN="$ROOT_DIR/dist/RelayHub-$APP_ARCH.app/Contents/Resources/relayhub-core"
    if [ ! -x "$CORE_BIN" ]; then
        echo "ERROR: bundle lacks Contents/Resources/relayhub-core (the shell cannot start a Core)"
        exit 1
    fi
fi

echo "==> Release verification passed successfully!"
