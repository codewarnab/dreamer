#!/bin/sh
# gofmt-changed.sh — fail if any .go file changed vs the diff base is not gofmt
# clean. Scopes to PR-introduced changes (not pre-existing tree dirt), which is
# exactly what reviewers asked for (PR #31, #57).
set -eu

base="$(sh scripts/diff-base.sh)"

# Added/copied/modified/renamed .go files vs the base.
files="$(git diff --name-only --diff-filter=ACMR "$base" -- '*.go')"
if [ -z "$files" ]; then
	exit 0
fi

# gofmt -l lists files that are NOT formatted. Filter to only the changed set.
unformatted=""
for f in $files; do
	[ -f "$f" ] || continue
	if [ -n "$(gofmt -l "$f")" ]; then
		unformatted="$unformatted $f"
	fi
done

if [ -n "$unformatted" ]; then
	echo "gofmt: the following changed files are not formatted:" >&2
	for f in $unformatted; do
		echo "  $f" >&2
	done
	echo "Run: gofmt -w$unformatted" >&2
	exit 1
fi
