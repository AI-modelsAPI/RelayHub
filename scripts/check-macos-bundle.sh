#!/usr/bin/env bash
# Installability gate for the packaged macOS desktop app.
#
#   scripts/check-macos-bundle.sh <RelayHub.app> [<RelayHub.dmg>]
#
# Checks the artifact the way macOS looks at it when a user installs it, and
# fails if an older-but-supported macOS would refuse it:
#   - every Mach-O in the bundle targets <= MACOS_MIN (LC_BUILD_VERSION minos),
#     and Info.plist declares LSMinimumSystemVersion == MACOS_MIN;
#   - the bundle carries a valid (ad-hoc) code signature seal;
#   - the DMG mounts, holds RelayHub.app next to an /Applications shortcut, and
#     the app inside it passes the same checks.
#
# build-macos.sh runs it after every build (local, CI, release). v0.1.0
# shipped a shell stamped minos 26.0 by the CI runner's SDK: it built, passed
# every test on the runner, and could not be opened on macOS 14, because
# nothing in the pipeline looked at the artifact from an older system's side.
set -euo pipefail

APP="${1:?usage: check-macos-bundle.sh <app> [<dmg>]}"
DMG="${2:-}"
MIN="${MACOS_MIN:-13.0}"

fail() { echo "ERROR: $*" >&2; exit 1; }

# version_le A B: A <= B for dotted numeric versions (13.0 <= 13.0.1 <= 14).
version_le() {
    [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | head -n1)" = "$1" ]
}

# Deployment target of a thin Mach-O: LC_BUILD_VERSION "minos", or the legacy
# LC_VERSION_MIN_MACOSX "version".
macho_minos() {
    otool -l "$1" | awk '
        /cmd LC_BUILD_VERSION/      { b = 1; next }
        /cmd LC_VERSION_MIN_MACOSX/ { v = 1; next }
        b && $1 == "minos"   { print $2; exit }
        v && $1 == "version" { print $2; exit }'
}

check_app() {
    local app="$1" label="$2"
    local plist="$app/Contents/Info.plist"
    [ -f "$plist" ] || fail "[$label] missing Contents/Info.plist"

    local exe lsmin
    exe="$(plutil -extract CFBundleExecutable raw "$plist" 2>/dev/null || true)"
    [ -n "$exe" ] || fail "[$label] Info.plist has no CFBundleExecutable"
    lsmin="$(plutil -extract LSMinimumSystemVersion raw "$plist" 2>/dev/null || true)"
    [ "$lsmin" = "$MIN" ] || fail "[$label] LSMinimumSystemVersion is '${lsmin:-<missing>}', expected $MIN"
    echo "  [$label] LSMinimumSystemVersion $lsmin"

    local bin rel minos
    for bin in "$app/Contents/MacOS/$exe" "$app/Contents/Resources/relayhub-core"; do
        rel="${bin#"$app"/}"
        [ -f "$bin" ] || fail "[$label] missing $rel"
        minos="$(macho_minos "$bin")"
        [ -n "$minos" ] || fail "[$label] cannot read the deployment target of $rel"
        version_le "$minos" "$MIN" \
            || fail "[$label] $rel requires macOS $minos but the floor is $MIN: macOS $MIN..$minos would refuse to open the app"
        echo "  [$label] $rel: minos $minos (<= $MIN)"
    done

    local out
    if ! out="$(codesign --verify --deep --strict --verbose=2 "$app" 2>&1)"; then
        fail "[$label] code signature does not verify (a downloaded copy is reported as \"damaged\"):
$out"
    fi
    echo "  [$label] code signature seal verifies"
}

check_app "$APP" "$(basename "$APP")"

if [ -n "$DMG" ]; then
    [ -f "$DMG" ] || fail "missing $DMG"
    MNT="$(mktemp -d "${TMPDIR:-/tmp}/relayhub-dmgcheck.XXXXXX")"
    cleanup() {
        hdiutil detach "$MNT" -quiet >/dev/null 2>&1 || hdiutil detach "$MNT" -force -quiet >/dev/null 2>&1 || true
        rmdir "$MNT" 2>/dev/null || true
    }
    trap cleanup EXIT
    hdiutil attach -nobrowse -readonly -noautoopen -mountpoint "$MNT" "$DMG" >/dev/null \
        || fail "cannot mount $DMG"
    [ -d "$MNT/RelayHub.app" ] \
        || fail "$(basename "$DMG"): no RelayHub.app at the volume root (found: $(ls -A "$MNT" | tr '\n' ' '))"
    if [ ! -L "$MNT/Applications" ] || [ "$(readlink "$MNT/Applications")" != "/Applications" ]; then
        fail "$(basename "$DMG"): missing the Applications shortcut to drag the app onto"
    fi
    echo "  [$(basename "$DMG")] RelayHub.app + Applications shortcut"
    check_app "$MNT/RelayHub.app" "$(basename "$DMG")"
fi

echo "==> $(basename "$APP") is installable on macOS $MIN and later"
