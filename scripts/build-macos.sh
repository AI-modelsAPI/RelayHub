#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"
mkdir -p "$DIST_DIR"

# Stamp the Core with the same metadata the Makefile injects, so a packaged
# app reports a real version/commit instead of "0.0.0-dev (commit unknown)"
# (AUDIT RH-33: release builds used bare -s -w).
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-s -w -X relayhub/internal/buildinfo.version=$VERSION -X relayhub/internal/buildinfo.commit=$COMMIT -X relayhub/internal/buildinfo.date=$DATE"
echo "==> Build metadata: version=$VERSION commit=$COMMIT date=$DATE"

build_arch() {
    local ARCH="$1"
    local DMG_ARCH=""
    local CLANG_ARCH=""
    if [ "$ARCH" = "arm64" ]; then
        DMG_ARCH="apple-silicon"
        CLANG_ARCH="arm64"
    elif [ "$ARCH" = "amd64" ]; then
        DMG_ARCH="intel"
        CLANG_ARCH="x86_64"
    else
        DMG_ARCH="$ARCH"
        CLANG_ARCH="$ARCH"
    fi
    local DMG_NAME="RelayHub-macOS-$DMG_ARCH.dmg"
    local APP_DIR="$DIST_DIR/RelayHub-$ARCH.app"

    echo "==> Building macOS bundle for $ARCH ($CLANG_ARCH)..."
    rm -rf "$APP_DIR"
    mkdir -p "$APP_DIR/Contents/MacOS"
    mkdir -p "$APP_DIR/Contents/Resources"

    # 1. Build Go Core binary into Contents/Resources/relayhub-core
    echo "  Building Go Core for $ARCH..."
    CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" go build -ldflags="$LDFLAGS" -o "$APP_DIR/Contents/Resources/relayhub-core" "$ROOT_DIR/cmd/relayhub"

    # 2. Compile native Objective-C/Cocoa desktop shell executable into Contents/MacOS/RelayHub
    echo "  Compiling native macOS shell for $CLANG_ARCH..."
    # Pin the deployment target. Without it clang stamps the SDK of the build
    # host (CI runs macos-latest = macOS 15+) into the binary and macOS 14
    # refuses to launch it ("cannot be used with this version of macOS").
    # 13.0 matches the floor of the Go Core (Go 1.27 requires macOS 13).
    clang -arch "$CLANG_ARCH" \
        -mmacosx-version-min=13.0 \
        -O2 \
        -fobjc-arc \
        -framework Cocoa \
        -framework WebKit \
        "$ROOT_DIR/desktop/macos/RelayHubApp/main.m" \
        -o "$APP_DIR/Contents/MacOS/RelayHub"

    # 3. Copy Plist and resources
    cp "$ROOT_DIR/desktop/macos/RelayHubApp/Info.plist" "$APP_DIR/Contents/"
    if [ -d "$ROOT_DIR/desktop/macos/RelayHubApp/Resources" ]; then
        cp -R "$ROOT_DIR/desktop/macos/RelayHubApp/Resources/"* "$APP_DIR/Contents/Resources/" 2>/dev/null || true
    fi

    echo "==> Packaging DMG for $ARCH..."
    if command -v hdiutil &>/dev/null; then
        rm -f "$DIST_DIR/$DMG_NAME"
        hdiutil create -volname "RelayHub" -srcfolder "$APP_DIR" -ov -format UDZO "$DIST_DIR/$DMG_NAME"
        echo "Created: $DIST_DIR/$DMG_NAME"
    else
        echo "hdiutil not found, skipping DMG creation"
    fi
}

# Build Apple Silicon (arm64) and Intel (amd64)
build_arch "arm64"
build_arch "amd64"

echo "==> macOS build complete!"
