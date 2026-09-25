#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"
mkdir -p "$DIST_DIR"

# Oldest macOS the desktop app supports: the single source for the clang
# deployment target, the bundle's LSMinimumSystemVersion and the post-build
# installability check. 13.0 is the floor of the Go 1.27 Core.
MACOS_MIN="${MACOS_MIN:-13.0}"
# Anything compiled for the bundle (clang, a future cgo) must target the
# floor, not the SDK of the build host: CI runs macos-latest, and without an
# explicit target v0.1.0 shipped a shell stamped minos 26.0 that macOS 14
# refused to open ("cannot be used with this version of macOS").
export MACOSX_DEPLOYMENT_TARGET="$MACOS_MIN"

# Stamp the Core with the same metadata the Makefile injects, so a packaged
# app reports a real version/commit instead of "0.0.0-dev (commit unknown)"
# (AUDIT RH-33: release builds used bare -s -w).
VERSION="${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-s -w -X relayhub/internal/buildinfo.version=$VERSION -X relayhub/internal/buildinfo.commit=$COMMIT -X relayhub/internal/buildinfo.date=$DATE"
# Finder's "Version" must be numeric x.y.z: v0.1.2-3-gabc1234 -> 0.1.2.
# Untagged builds (bare commit hash) report 0.0.0 rather than a bogus number.
if [[ "$VERSION" =~ ^v?([0-9]+\.[0-9]+\.[0-9]+) ]]; then
    BUNDLE_VERSION="${BASH_REMATCH[1]}"
else
    BUNDLE_VERSION="0.0.0"
fi
echo "==> Build metadata: version=$VERSION commit=$COMMIT date=$DATE bundle=$BUNDLE_VERSION macos>=$MACOS_MIN"

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
    # -mmacosx-version-min pins LC_BUILD_VERSION.minos to the floor (see
    # MACOS_MIN). -Werror=unguarded-availability-new makes that floor honest:
    # calling an API newer than the floor without an @available check fails
    # the build instead of crashing on the oldest supported macOS.
    clang -arch "$CLANG_ARCH" \
        -mmacosx-version-min="$MACOS_MIN" \
        -Werror=unguarded-availability-new \
        -O2 \
        -fobjc-arc \
        -framework Cocoa \
        -framework WebKit \
        "$ROOT_DIR/desktop/macos/RelayHubApp/main.m" \
        -o "$APP_DIR/Contents/MacOS/RelayHub"

    # 3. Copy Plist and resources; stamp the plist from the build variables so
    #    it can never disagree with the binaries.
    cp "$ROOT_DIR/desktop/macos/RelayHubApp/Info.plist" "$APP_DIR/Contents/"
    plutil -replace LSMinimumSystemVersion -string "$MACOS_MIN" "$APP_DIR/Contents/Info.plist"
    plutil -replace CFBundleShortVersionString -string "$BUNDLE_VERSION" "$APP_DIR/Contents/Info.plist"
    plutil -replace CFBundleVersion -string "$BUNDLE_VERSION" "$APP_DIR/Contents/Info.plist"
    if [ -d "$ROOT_DIR/desktop/macos/RelayHubApp/Resources" ]; then
        cp -R "$ROOT_DIR/desktop/macos/RelayHubApp/Resources/"* "$APP_DIR/Contents/Resources/" 2>/dev/null || true
    fi

    # 4. Ad-hoc sign, inside out (nested Core first, then the bundle seal).
    #    There is no Developer ID certificate, so this is not notarization, but
    #    it matters: without a valid seal Gatekeeper reports a downloaded app
    #    as "damaged and can't be opened" on Apple Silicon (whose linker-signed
    #    executables do not cover Info.plist/resources) and offers no way to
    #    open it. With the seal the user gets the normal "unidentified
    #    developer" prompt that right-click > Open (or Privacy & Security >
    #    Open Anyway) resolves.
    echo "  Ad-hoc signing bundle..."
    codesign --force --sign - --timestamp=none --identifier com.relayhub.core \
        "$APP_DIR/Contents/Resources/relayhub-core"
    codesign --force --sign - --timestamp=none "$APP_DIR"

    echo "==> Packaging DMG for $ARCH..."
    local DMG_PATH=""
    if command -v hdiutil &>/dev/null; then
        # The volume holds RelayHub.app plus an Applications shortcut, so the
        # user drags the app onto the shortcut and ends up with
        # /Applications/RelayHub.app, the path the LaunchAgent and the release
        # notes use. (Before: -srcfolder pointed at RelayHub-<arch>.app, so the
        # installed app was named RelayHub-amd64.app and there was no target
        # to drag it to.)
        local STAGE
        STAGE="$(mktemp -d "${TMPDIR:-/tmp}/relayhub-dmg.XXXXXX")"
        ditto "$APP_DIR" "$STAGE/RelayHub.app"
        ln -s /Applications "$STAGE/Applications"
        rm -f "$DIST_DIR/$DMG_NAME"
        hdiutil create -volname "RelayHub" -srcfolder "$STAGE" -ov -format UDZO "$DIST_DIR/$DMG_NAME"
        rm -rf "$STAGE"
        DMG_PATH="$DIST_DIR/$DMG_NAME"
        echo "Created: $DMG_PATH"
    else
        echo "hdiutil not found, skipping DMG creation"
    fi

    # 5. Look at the artifact the way macOS will before anyone ships it.
    MACOS_MIN="$MACOS_MIN" "$ROOT_DIR/scripts/check-macos-bundle.sh" "$APP_DIR" ${DMG_PATH:+"$DMG_PATH"}
}

# Build Apple Silicon (arm64) and Intel (amd64)
build_arch "arm64"
build_arch "amd64"

echo "==> macOS build complete!"
