# RelayHub macOS Desktop Application

A native (Objective-C/Cocoa + WebKit) menu bar shell that owns the RelayHub
Core process lifecycle: `desktop/macos/RelayHubApp/main.m`.

## What the shell actually does

- **Owns the Core**: launches `Contents/Resources/relayhub-core -full-stack`
  with the chosen data dir and management port, records an ownership token
  (`<data-dir>/relayhub.token`: pid, UUID, start time) and only ever signals a
  process whose identity and start time still match.
- **Single instance**: `flock` on `<data-dir>/relayhub.lock`; a second shell on
  the same data dir attaches in observer mode only if the port answers a
  genuine RelayHub `/api/v1/health`.
- **Fail-closed on port conflict**: if the port is taken by something that is
  not RelayHub, no Core is started and no page is loaded.
- **Watchdog**: a crashed Core is reported (modal, suppressible with
  `RELAYHUB_NO_ALERT_MODAL=1` for tests).
- **Menu bar**: show dashboard / open in browser / reload / quit. There is no
  start/stop/restart menu — the Core lives and dies with the shell.

## Not (yet) wired

- `KeychainStore.swift` and `AppDelegate.swift` are reference sources; the
  shipped shell is `main.m` and the master key is a `0600` file under the data
  dir (`internal/secrets.FileKeyProvider`). Keychain-backed key storage is a
  documented option, not a delivered feature.

## Building

```bash
./scripts/build-macos.sh      # both arches, version-stamped Core
```

Outputs:
- `dist/RelayHub-macOS-apple-silicon.dmg` / `dist/RelayHub-arm64.app`
- `dist/RelayHub-macOS-intel.dmg` / `dist/RelayHub-amd64.app`

Each DMG volume holds `RelayHub.app` next to an `Applications` shortcut, so
dragging one onto the other installs `/Applications/RelayHub.app` (the path the
LaunchAgent below expects).

What the build guarantees about the artifact:

- **Minimum macOS 13** (`MACOS_MIN`, the floor of the Go 1.27 Core). The shell
  is compiled with `-mmacosx-version-min=$MACOS_MIN` and
  `-Werror=unguarded-availability-new`, and the bundle's
  `LSMinimumSystemVersion` is stamped from the same variable. Without an
  explicit target clang uses the build host's SDK: v0.1.0 was built on
  `macos-latest` and its shell required macOS 26, so macOS 14 refused it
  ("cannot be used with this version of macOS").
- **Ad-hoc signature seal** over the Core and the bundle. This is not a
  Developer ID signature (no certificate, no notarization), but without a
  valid seal Gatekeeper calls a downloaded Apple Silicon build "damaged" and
  offers no way to open it.
- `scripts/check-macos-bundle.sh` runs after every build (local, CI, release)
  and fails on any Mach-O with a deployment target above the floor, a
  mismatched `LSMinimumSystemVersion`, a seal that does not verify, or a DMG
  without `RelayHub.app` + `Applications`.

## Installing

1. Open the DMG for your CPU (`apple-silicon` or `intel`) and drag RelayHub
   onto the Applications shortcut.
2. The first launch is blocked by Gatekeeper because the app is not notarized:
   - macOS 13 / 14: right-click RelayHub in Finder, choose Open, then Open.
   - macOS 15 and later: try to open it once, then click **Open Anyway** in
     System Settings > Privacy & Security.
   - Or: `xattr -dr com.apple.quarantine /Applications/RelayHub.app`

Check what macOS will think of a build before shipping it:

```bash
vtool -show-build dist/RelayHub-amd64.app/Contents/MacOS/RelayHub   # minos 13.0
codesign --verify --deep --strict --verbose=2 dist/RelayHub-amd64.app
syspolicy_check distribution dist/RelayHub-amd64.app                  # only notarization errors expected
```

## Real-device tests

```bash
make test-macos               # = ./tests/macos/test_full_suite.sh
```

1. `tests/macos/smoke.sh` — builds, validates bundle layout, `lipo` arch, DMG
   names, and launches the native-arch binary.
2. `scripts/verify-release.sh` — version token parity between the host binary
   and the packaged app, secret-pattern scan, Core presence in the bundle.
3. `tests/macos/test_desktop_lifecycle.py` — `unittest` suite against the
   native-arch `.app`: ownership token + PPID, `/healthz`, UI, extras API,
   fail-closed foreign port, flock contention, crash watchdog.

## Launch at login (optional)

```bash
cp desktop/macos/LaunchAgent.plist ~/Library/LaunchAgents/com.relayhub.desktop.plist
launchctl load ~/Library/LaunchAgents/com.relayhub.desktop.plist
```

The agent starts the **shell** (`/Applications/RelayHub.app/Contents/MacOS/RelayHub`),
never the Core directly, so the ownership/watchdog logic above still applies.
