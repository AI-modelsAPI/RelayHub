#!/bin/sh
set -e

# Probe management API healthz.
# Default to 127.0.0.1:8790, or allow override via RELAYHUB_HEALTH_URL or RELAYHUB_MANAGEMENT_ADDR.
TARGET_URL="${RELAYHUB_HEALTH_URL:-}"
if [ -z "$TARGET_URL" ]; then
    MGMT_ADDR="${RELAYHUB_MANAGEMENT_ADDR:-127.0.0.1:8790}"
    TARGET_URL="http://${MGMT_ADDR}/healthz"
fi

BODY=$(curl --noproxy '*' --connect-timeout 1 --max-time 3 -fsS "$TARGET_URL") || exit 1
STATUS=$(printf '%s' "$BODY" | grep -o '"status":"ok"' || true)
if [ -n "$STATUS" ]; then
    exit 0
fi

exit 1
