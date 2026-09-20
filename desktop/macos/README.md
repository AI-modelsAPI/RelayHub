# RelayHub macOS Desktop Application

A lightweight native macOS menu bar wrapper for RelayHub Core.

## Features

- **Menu Bar Resident**: Control Core service state (Start / Stop / Restart) from the macOS menu bar.
- **One-Click Console**: Quick access to the local Web Management Console (`http://127.0.0.1:8790`).
- **Keychain Storage Bridge**: Uses Apple Keychain Services for zero-plaintext master secret protection.
- **Background Autostart**: Optional launch on login via `LaunchAgent.plist`.
- **Universal Architecture**: Pre-configured build pipelines for Apple Silicon (`arm64`) and Intel (`x86_64`).

## Building macOS DMGs

Run the automated build script:

```bash
./scripts/build-macos.sh
```

Outputs:
- `dist/RelayHub-macOS-apple-silicon.dmg`
- `dist/RelayHub-macOS-intel.dmg`
