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
