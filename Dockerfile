# Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
# internal/web/ carries the embedded frontend (see internal/web/embed.go). The
# top-level web/ copy is editable source that never reaches the binary, so it is
# deliberately not copied here.
COPY internal/ internal/

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/relayhub ./cmd/relayhub

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

ENV DATA_DIR="/data" \
    RELAYHUB_MANAGEMENT_ADDR="127.0.0.1:8790"

VOLUME ["/data"]
USER 10001:10001

EXPOSE 8787 8788 8789 8790

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/app/healthcheck.sh"]

ENTRYPOINT ["/app/entrypoint.sh"]
CMD []
