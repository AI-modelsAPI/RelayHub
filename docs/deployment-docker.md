# Docker Deployment Guide

## Running RelayHub in Docker

### With Docker Compose (Recommended)

```bash
docker compose up -d
```

### With Docker CLI

```bash
docker run -d \
  --name relayhub \
  -p 8787:8787 \
  -p 8788:8788 \
  -p 8789:8789 \
  -p 127.0.0.1:8790:8790 \
  -v relayhub-data:/data \
  relayhub:latest
```

## Volumes & Directory Structure

- `/data/relayhub.db`: SQLite database holding resources, check-in records, and audit logs.
- `/data/master.key`: 32-byte secret encryption key with `0600` permissions.
