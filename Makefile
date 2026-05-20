.PHONY: build build-dev build-linux test test-race vet fmt clean

# Release build: stripped binary (~18MB, no debug symbols)
# -trimpath removes local filesystem paths from the binary so stack traces
# and debug symbols don't leak the build machine's directory structure.
# Also makes builds reproducible (same source → identical binary hash).
build:
	go build -trimpath -ldflags="-s -w" -o dreamer.exe .

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
	go build -trimpath -o dreamer.exe .

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

# Remove built binaries
clean:
	rm -f dreamer.exe dreamer dreamer-stripped.exe
