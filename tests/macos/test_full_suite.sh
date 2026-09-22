#!/usr/bin/env bash
# Real-device macOS acceptance suite. Run on a Mac with Xcode CLT + Go 1.27:
#   ./tests/macos/test_full_suite.sh
# Every step exercises the actual packaged artifact, not a unit stub.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT_DIR"

echo "========================================================"
echo "1. Packaging smoke: build both arches, validate .app/.dmg (tests/macos/smoke.sh)"
echo "========================================================"
./tests/macos/smoke.sh

echo "========================================================"
echo "2. Release verification: version, secret scan, app/core version parity (scripts/verify-release.sh)"
echo "========================================================"
./scripts/verify-release.sh

echo "========================================================"
echo "3. Desktop lifecycle on the native-arch bundle (tests/macos/test_desktop_lifecycle.py)"
echo "========================================================"
python3 -m unittest -v tests.macos.test_desktop_lifecycle

echo "========================================================"
echo "ALL macOS REAL-DEVICE SUITES PASSED"
echo "========================================================"
