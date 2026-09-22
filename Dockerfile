# Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates

# Module proxy is overridable for networks where proxy.golang.org is blocked
# (e.g. --build-arg GOPROXY=https://goproxy.cn,direct). Default is unchanged.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
# internal/web/ carries the embedded frontend (see internal/web/embed.go). The
# top-level web/ copy is editable source that never reaches the binary, so it is
# deliberately not copied here.
COPY internal/ internal/

# Build metadata is passed in by CI (docker/build-push-action build-args) so
# the image reports a real version like every other artifact (AUDIT RH-33).
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X relayhub/internal/buildinfo.version=${VERSION} -X relayhub/internal/buildinfo.commit=${COMMIT} -X relayhub/internal/buildinfo.date=${DATE}" \
    -o /bin/relayhub ./cmd/relayhub

# Final minimal runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata curl bash && \
    addgroup -g 10001 relayhub && \
    adduser -u 10001 -G relayhub -D -h /data relayhub

WORKDIR /app
COPY --from=builder /bin/relayhub /app/relayhub
COPY docker/entrypoint.sh /app/entrypoint.sh
COPY docker/healthcheck.sh /app/healthcheck.sh

RUN chmod +x /app/entrypoint.sh /app/healthcheck.sh && \
    mkdir -p /data && \
    chown -R relayhub:relayhub /data /app

# Listen addresses. The management plane is loopback-only by design (reach it
# with `docker exec` or run the container with host networking). The three
# data-plane listeners default to loopback too; publish them through a bridge
# network by overriding these to 0.0.0.0:<port> (AUDIT RH-04) — see
# docs/deployment-docker.md. The gateway is protected by local API keys; the
# proxies are NOT authenticated unless configured, so never publish them to a
# non-loopback host interface.
ENV DATA_DIR="/data" \
    RELAYHUB_MANAGEMENT_ADDR="127.0.0.1:8790" \
    RELAYHUB_HTTP_PROXY_ADDR="127.0.0.1:8787" \
    RELAYHUB_SOCKS5_ADDR="127.0.0.1:8788" \
    RELAYHUB_GATEWAY_ADDR="127.0.0.1:8789"

VOLUME ["/data"]
USER 10001:10001

EXPOSE 8787 8788 8789 8790

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/app/healthcheck.sh"]

ENTRYPOINT ["/app/entrypoint.sh"]
CMD []
