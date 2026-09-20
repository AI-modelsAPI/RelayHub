#!/usr/bin/env bash
set -euo pipefail

echo "========================================================"
echo "1. Smoke test: Build and package check (x86_64 + arm64)"
echo "========================================================"
./tests/macos/smoke.sh

echo "========================================================"
echo "2. Acceptance Probe (packaging_probe.py)"
echo "========================================================"
python3 docs/acceptance/packaging_probe.py

echo "========================================================"
echo "3. Desktop Lifecycle Suite (tests/macos/test_desktop_lifecycle.py)"
echo "========================================================"
python3 tests/macos/test_desktop_lifecycle.py

echo "========================================================"
echo "ALL TEST SUITES PASSED SUCCESSFULLY!"
echo "========================================================"
