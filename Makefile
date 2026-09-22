# RelayHub Core Makefile.
#
# Bootstrap-stage targets. Build metadata is injected via -ldflags so the
# binary reports an accurate version, commit, and build date.

BINARY      := relayhub
CMD_PKG     := ./cmd/relayhub
BIN_DIR     := bin
BUILDINFO   := relayhub/internal/buildinfo

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(BUILDINFO).version=$(VERSION) \
           -X $(BUILDINFO).commit=$(COMMIT) \
           -X $(BUILDINFO).date=$(DATE)

.DEFAULT_GOAL := build

.PHONY: format test vet build smoke clean check-web test-real-device test-macos vuln

## format: rewrite Go source with gofmt (same scope CI checks: cmd internal tests).
format:
	gofmt -w cmd internal tests

## test: run the unit tests, then verify the web assets have not diverged.
test: check-web test-web
	go test ./... -count=1

## check-web: fail if web/ and internal/web/ differ.
## internal/web/ is the copy embedded into the binary; web/ is the editable
## source. They are byte-identical by contract, and letting them drift has
## already shipped stale assets once. Run `make sync-web` after editing.
check-web:
	@for f in index.html app.js styles.css; do \
		if ! diff -q web/$$f internal/web/$$f >/dev/null 2>&1; then \
			echo "web asset drift: web/$$f and internal/web/$$f differ; run 'make sync-web'"; \
			exit 1; \
		fi; \
	done
	@echo "web assets in sync"

## sync-web: copy the editable frontend into the embedded location.
sync-web:
	@cp web/index.html web/app.js web/styles.css internal/web/
	@echo "synced web/ -> internal/web/"

.PHONY: test-web
test-web:
	node --test tests/web/*.test.cjs

## vet: run go vet across all packages.
vet:
	go vet ./...

## build: compile the relayhub binary with stamped build info.
build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD_PKG)

## smoke: build the binary and confirm it reports its version.
smoke: build
	$(BIN_DIR)/$(BINARY) -version

## clean: remove build artifacts.
clean:
	rm -rf $(BIN_DIR) dist

## test-real-device: on-machine acceptance — unit/race suite, live full-stack
## smoke over real sockets, and on macOS the packaged-app lifecycle suite.
## Needs Go 1.27, curl, python3; macOS additionally needs Xcode CLT.
test-real-device: check-web test-web
	go test -race ./... -count=1 -timeout=15m
	./scripts/smoke-local.sh
	@if [ "$$(uname -s)" = "Darwin" ]; then $(MAKE) test-macos; else echo "skipping macOS desktop suite (host is not Darwin)"; fi

## test-macos: build both arches, package DMGs, verify, and run the desktop
## shell lifecycle tests against the native-arch bundle.
test-macos:
	./tests/macos/test_full_suite.sh

## vuln: Go dependency vulnerability scan (needs network).
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
