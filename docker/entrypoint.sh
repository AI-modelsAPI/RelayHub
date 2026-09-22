#!/bin/sh
set -e

# Data directory can be configured via DATA_DIR environment variable (default: /data).
DATA_DIR="${DATA_DIR:-/data}"
mkdir -p "$DATA_DIR"

# If the caller didn't supply arguments, start with full-stack and configured DATA_DIR
if [ "$#" -eq 0 ]; then
    set -- -full-stack -data-dir "$DATA_DIR"
fi

# Log to stderr: stdout must stay clean so `docker run relayhub -version`
# (release verification, CI version assertion) sees only the version line.
echo "==> Starting RelayHub Core in container (UID: $(id -u))..." >&2
exec /app/relayhub "$@"
