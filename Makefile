.PHONY: build build-dev build-linux dev install test test-race vet fmt lint cover cover-html vulncheck quality quality-json tools install-hooks clean

# Auto-detect host OS/arch via the active Go toolchain.
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
EXE    := $(if $(filter windows,$(GOOS)),.exe,)
BIN    := dreamer$(EXE)

# Version injection — git tag or "dev" when no tags exist.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X dreamer/cmd.version=$(VERSION) -X dreamer/cmd.commit=$(COMMIT) -X dreamer/cmd.date=$(DATE)
DEV_LDFLAGS := -X dreamer/cmd.version=$(VERSION) -X dreamer/cmd.commit=$(COMMIT) -X dreamer/cmd.date=$(DATE)

# Release build: stripped binary (~18MB, no debug symbols)
# -trimpath removes local filesystem paths from the binary so stack traces
# and debug symbols don't leak the build machine's directory structure.
# Also makes builds reproducible (same source → identical binary hash).
build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) .

# Optional: further compress with UPX (~6-8MB). Not default — may trigger
# antivirus false positives and adds ~50ms startup overhead.
# Install: scoop install upx (Windows) or apt install upx (Linux)
# compress: build
# 	upx --best dreamer.exe

# SQLite driver note: we use modernc.org/sqlite (pure Go, no CGo).
# mattn/go-sqlite3 would save ~3MB but requires a C compiler (MinGW on
# Windows), breaks easy cross-compilation, and adds CI toolchain friction.
# Pure-Go SQLite is the right trade-off — zero CGo, zero surprises.

# Development build: full debug symbols (for delve/dlv)
# -trimpath included so dev builds don't leak local paths either.
build-dev:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="$(DEV_LDFLAGS)" -o $(BIN) .

# Cross-compile for Linux
build-linux:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dreamer .

# Live-reload daemon during development (requires: go install github.com/air-verse/air@latest)
dev:
	air -c .air.toml

# Build + install to $GOPATH/bin (makes "dreamer" available on PATH)
install:
	go install -trimpath -ldflags="$(LDFLAGS)" .

# Run full test suite
test:
	go test ./...

# Run tests with race detector
test-race:
	go test -race ./...

# Vet and format
vet:
	go vet ./...

fmt:
	gofmt -w .

# Lint: run golangci-lint (auto-installs if missing)
GOLANGCI_LINT_VERSION := v2.1.6
GOPATH_BIN := $(shell go env GOPATH)/bin
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found, installing $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
		$(GOPATH_BIN)/golangci-lint run ./...; \
	fi

# Test coverage
cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

cover-html: cover
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage.html written"

# Vulnerability check (auto-installs if missing)
GOVULNCHECK_VERSION := v1.1.4
vulncheck:
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		echo "govulncheck not found, installing $(GOVULNCHECK_VERSION)..."; \
		go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
		$(GOPATH_BIN)/govulncheck ./...; \
	fi

# Code quality: dreamer-specific type-aware analyzers (internal/astcheck)
# Full scan, baseline-aware (fails only on new findings vs .quality-baseline.json)
quality:
	go run ./tools/quality --baseline .quality-baseline.json
quality-json:
	go run ./tools/quality --json
quality-baseline:
	go run ./tools/quality --write-baseline

# Install developer tools used by the git hooks (pinned versions).
# gofumpt/goimports/gitleaks/lefthook install into $GOPATH/bin.
GOFUMPT_VERSION   := v0.7.0
GOIMPORTS_VERSION := latest
GITLEAKS_VERSION  := v8.30.1
LEFTHOOK_VERSION  := v1.7.22
tools:
	go install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)
	go install github.com/gitleaks/gitleaks/v8@$(GITLEAKS_VERSION)
	go install github.com/evilmartians/lefthook@$(LEFTHOOK_VERSION)
	@echo "dev tools installed to $(GOPATH_BIN)"

# Install git hooks via lefthook (reads lefthook.yml). Falls back to a clear
# message if lefthook isn't on PATH yet — run `make tools` first.
install-hooks:
	@if command -v lefthook >/dev/null 2>&1; then \
		lefthook install; \
		echo "git hooks installed (lefthook.yml)"; \
	else \
		echo "lefthook not found — run 'make tools' first, then 'make install-hooks'"; \
		exit 1; \
	fi

# Remove built binaries
clean:
	rm -f dreamer dreamer.exe dreamer-stripped.exe coverage.out coverage.html
