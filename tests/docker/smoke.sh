#!/usr/bin/env bash
set -euo pipefail

IMAGE_NAME="${1:-relayhub:test}"
CONTAINER_NAME="relayhub-smoke-test-$$"
TEMP_VOL="relayhub-smoke-vol-$$"

cleanup() {
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
    docker volume rm "$TEMP_VOL" 2>/dev/null || true
}
trap cleanup EXIT

echo "==> Testing container startup with volume..."
docker volume create "$TEMP_VOL"

docker run -d --name "$CONTAINER_NAME" \
    -v "$TEMP_VOL:/data" \
    "$IMAGE_NAME"

echo "==> Checking process non-root UID..."
UID_OUT=$(docker exec "$CONTAINER_NAME" id -u)
if [ "$UID_OUT" != "10001" ]; then
    echo "ERROR: Expected UID 10001, got $UID_OUT"
    exit 1
fi

echo "==> Waiting for container healthcheck..."
for i in $(seq 1 15); do
    if docker exec "$CONTAINER_NAME" /app/healthcheck.sh 2>/dev/null; then
        echo "Healthcheck OK!"
        break
    fi
    if [ "$i" -eq 15 ]; then
        echo "ERROR: Healthcheck timed out"
        docker logs "$CONTAINER_NAME"
        exit 1
    fi
    sleep 1
done

echo "==> Docker smoke test passed successfully!"
