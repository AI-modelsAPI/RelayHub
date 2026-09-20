#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

echo "==> Running macOS build smoke test..."
"$ROOT_DIR/scripts/build-macos.sh"

HOST_ARCH="$(uname -m)"
echo "==> Validating created .app bundles and DMGs (Host architecture: $HOST_ARCH)..."

for ARCH in "arm64" "amd64"; do
    APP="$ROOT_DIR/dist/RelayHub-$ARCH.app"
    PLIST="$APP/Contents/Info.plist"

    # 1. App bundle existence
    if [ ! -d "$APP" ]; then
        echo "ERROR: Missing app bundle $APP"
        exit 1
    fi

    # 2. Info.plist existence
    if [ ! -f "$PLIST" ]; then
        echo "ERROR: Missing Info.plist in $APP"
        exit 1
    fi

    # 3. CFBundleExecutable check & executable resolution (P2-9 defense)
    EXE_NAME="$(plutil -extract CFBundleExecutable raw "$PLIST" 2>/dev/null || true)"
    if [ -z "$EXE_NAME" ]; then
        echo "ERROR: CFBundleExecutable key not found in $PLIST"
        exit 1
    fi

    BIN="$APP/Contents/MacOS/$EXE_NAME"
    if [ ! -f "$BIN" ] || [ ! -x "$BIN" ]; then
        echo "ERROR: CFBundleExecutable '$EXE_NAME' missing or non-executable binary at $BIN"
        exit 1
    fi
    echo "  [$ARCH] CFBundleExecutable '$EXE_NAME' found and executable: $BIN"

    # 4. Architecture validation via lipo (or file fallback)
    EXPECTED_ARCH=""
    if [ "$ARCH" = "arm64" ]; then
        EXPECTED_ARCH="arm64"
    elif [ "$ARCH" = "amd64" ]; then
        EXPECTED_ARCH="x86_64"
    fi

    if command -v lipo &>/dev/null; then
        ACTUAL_ARCH="$(lipo -archs "$BIN")"
        if [ "$ACTUAL_ARCH" != "$EXPECTED_ARCH" ]; then
            echo "ERROR: Architecture mismatch for $BIN. Expected '$EXPECTED_ARCH', got '$ACTUAL_ARCH'"
            exit 1
        fi
        echo "  [$ARCH] Verified architecture via lipo: $ACTUAL_ARCH"
    else
        FILE_INFO="$(file "$BIN")"
        if ! echo "$FILE_INFO" | grep -q "$EXPECTED_ARCH"; then
            echo "ERROR: Architecture mismatch for $BIN. Expected '$EXPECTED_ARCH' in '$FILE_INFO'"
            exit 1
        fi
        echo "  [$ARCH] Verified architecture via file: $FILE_INFO"
    fi

    # 5. DMG artifact naming check (P2-8 defense)
    DMG_ARCH=""
    if [ "$ARCH" = "arm64" ]; then
        DMG_ARCH="apple-silicon"
    elif [ "$ARCH" = "amd64" ]; then
        DMG_ARCH="intel"
    fi
    DMG="$ROOT_DIR/dist/RelayHub-macOS-$DMG_ARCH.dmg"
    if [ ! -f "$DMG" ]; then
        echo "ERROR: Missing expected DMG artifact $DMG"
        exit 1
    fi
    echo "  [$ARCH] Verified DMG artifact: $DMG"

    # 6. Launchability check (native architecture execution, P2-10)
    # Check if host architecture can run target ARCH
    CAN_RUN=false
    if [ "$ARCH" = "amd64" ] && { [ "$HOST_ARCH" = "x86_64" ] || [ "$HOST_ARCH" = "i386" ]; }; then
        CAN_RUN=true
    elif [ "$ARCH" = "arm64" ] && [ "$HOST_ARCH" = "arm64" ]; then
        CAN_RUN=true
    fi

    if [ "$CAN_RUN" = true ]; then
        echo "  [$ARCH] Host matches target architecture, testing executable launch..."
        if ! APP_VER="$("$BIN" -version 2>&1)"; then
            echo "ERROR: $BIN -version failed: $APP_VER"
            exit 1
        fi
        if [[ "$APP_VER" != relayhub\ * ]]; then
            echo "ERROR: Unexpected Core version output: $APP_VER"
            exit 1
        fi
        echo "  [$ARCH] Successfully launched executable: $APP_VER"
    else
        echo "  [$ARCH] Skipping execution test (host '$HOST_ARCH' != target '$ARCH')"
    fi
done

echo "==> macOS packaging smoke test passed successfully!"
