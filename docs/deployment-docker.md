# Docker Deployment Guide

RelayHub is local-first: inside the container every listener binds loopback
(`127.0.0.1`). That is the right default for a native install, but with a
Docker *bridge* network it means `-p 8789:8789` publishes a port nothing
answers on — the host forwards to the container's eth0, while the socket only
listens on the container's own `lo` (audit finding RH-04). Pick one of the two
deployment shapes below.

## Option A — host networking (recommended, Linux / Docker Desktop ≥ 4.34)

```bash
docker compose up -d
```

`docker-compose.yml` uses `network_mode: host`, so the four services appear on
the host's `127.0.0.1:8787/8788/8789/8790` exactly like a native binary.
Nothing is reachable from other machines.

## Option B — bridge network with published data-plane ports

Bind the three data-plane listeners to the container's interfaces and publish
them **only on the host's loopback**:

```bash
docker run -d \
  --name relayhub \
  -e RELAYHUB_HTTP_PROXY_ADDR=0.0.0.0:8787 \
  -e RELAYHUB_SOCKS5_ADDR=0.0.0.0:8788 \
  -e RELAYHUB_GATEWAY_ADDR=0.0.0.0:8789 \
  -p 127.0.0.1:8787:8787 \
  -p 127.0.0.1:8788:8788 \
  -p 127.0.0.1:8789:8789 \
  -v relayhub-data:/data \
  relayhub:latest
```

The management API / Web UI (`8790`) is loopback-only **by design** and cannot
be rebound; reach it from inside the container:

```bash
docker exec relayhub curl -s http://127.0.0.1:8790/api/v1/overview
```

Never publish `8787`/`8788` to a non-loopback host interface: the proxies are
unauthenticated unless `Username`/`Password` are configured, and an open proxy
on a LAN is a relay for anyone on it. The gateway (`8789`) requires a local API
key, but treat it the same way.

## Listen address reference

| Variable / flag | Default | Notes |
|---|---|---|
| `RELAYHUB_MANAGEMENT_ADDR` / `-management-addr` | `127.0.0.1:8790` | Must be loopback; the process refuses anything else |
| `RELAYHUB_HTTP_PROXY_ADDR` / `-http-proxy-addr` | `127.0.0.1:8787` | Any IP host, `localhost`, or `:port` for all interfaces |
| `RELAYHUB_SOCKS5_ADDR` / `-socks5-addr` | `127.0.0.1:8788` | same |
| `RELAYHUB_GATEWAY_ADDR` / `-gateway-addr` | `127.0.0.1:8789` | same; the address advertised to CLI sync is always dialable (`0.0.0.0` → `127.0.0.1`) |

A non-loopback listener is logged as a `WARNING` at startup.

## Volumes & Directory Structure

- `/data/relayhub.db`: SQLite database holding resources, check-in records, and audit logs.
- `/data/master.key`: 32-byte secret encryption key with `0600` permissions.

## Building behind a restricted network

`go mod download` inside the build stage talks to `proxy.golang.org`. Where
that host is unreachable, point the build at a mirror:

```bash
docker build --build-arg GOPROXY=https://goproxy.cn,direct \
  --build-arg VERSION=$(git describe --tags --always --dirty) \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t relayhub:latest .
```

## Verified on macOS with Colima

Both shapes above were exercised on a Mac running Docker via Colima
(`colima start --vm-type vz`): in host-network mode the sockets live on the
Colima VM's loopback and Colima forwards them to the Mac's `127.0.0.1`, so
`curl http://127.0.0.1:8790/healthz` works from the Mac; in bridge mode the
published ports reach the container's `0.0.0.0` listeners and traffic
provably transits the container (the container-internal management API is
reachable *only* through the container's proxies).

## Verifying an image

```bash
docker run --rm relayhub:latest -version   # must not report 0.0.0-dev / commit unknown
./tests/docker/smoke.sh relayhub:latest    # non-root UID + healthcheck
```
