#!/bin/sh
# diff-base.sh — print the git revision to diff the current work against.
#
# Prefers origin/main. On a fresh or shallow clone where that ref is absent,
# fetches it; failing that, falls back to a local main, then HEAD. Centralizing
# this here keeps every hook (golangci --new-from-rev, quality --diff-from,
# gofmt-changed) from hardcoding origin/main and erroring on a fresh clone
# (recurring CI breakage — see PR #57, #60).
set -eu

if git rev-parse --verify --quiet origin/main >/dev/null 2>&1; then
	echo origin/main
	exit 0
fi

# Try to populate the ref. Best-effort: offline clones still fall through.
git fetch --quiet origin main >/dev/null 2>&1 || true

if git rev-parse --verify --quiet origin/main >/dev/null 2>&1; then
	echo origin/main
elif git rev-parse --verify --quiet main >/dev/null 2>&1; then
	echo main
else
	echo HEAD
fi
