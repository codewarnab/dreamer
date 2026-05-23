.PHONY: build build-dev build-linux test test-race vet fmt lint cover cover-html vulncheck install-hooks clean

# Auto-detect host OS/arch via the active Go toolchain.
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
EXE    := $(if $(filter windows,$(GOOS)),.exe,)
BIN    := dreamer$(EXE)

# Release build: stripped binary (~18MB, no debug symbols)
# -trimpath removes local filesystem paths from the binary so stack traces
# and debug symbols don't leak the build machine's directory structure.
# Also makes builds reproducible (same source → identical binary hash).
build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="-s -w" -o $(BIN) .

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
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -o $(BIN) .

# Cross-compile for Linux
build-linux:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dreamer .

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
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found, installing $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
		golangci-lint run ./...; \
	fi

# Test coverage
cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

cover-html: cover
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage.html written"

# Vulnerability check (auto-installs if missing)
vulncheck:
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		echo "govulncheck not found, installing..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
		govulncheck ./...; \
	fi

# Install git hooks
install-hooks:
	@cp .githooks/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook installed"

# Remove built binaries
clean:
	rm -f dreamer dreamer.exe dreamer-stripped.exe coverage.out coverage.html
