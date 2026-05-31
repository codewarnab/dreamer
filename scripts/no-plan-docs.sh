#!/bin/sh
# no-plan-docs.sh — enforce CLAUDE.md rule #10: don't ship plan/audit docs
# alongside implementation. Fails when the STAGED diff both touches Go code and
# adds/edits a planning doc (PLANS/**, root findings.md, *-plan.md, *-audit.md).
# Such docs are instantly stale and read as scope creep in the same PR
# (recurring review finding — PR #47, #57, #60).
set -eu

staged="$(git diff --cached --name-only --diff-filter=ACMR)"
[ -z "$staged" ] && exit 0

has_go=0
plan_docs=""
for f in $staged; do
	case "$f" in
	*.go) has_go=1 ;;
	esac
	case "$f" in
	PLANS/* | findings.md | *-plan.md | *-audit.md)
		plan_docs="$plan_docs $f"
		;;
	esac
done

if [ "$has_go" -eq 1 ] && [ -n "$plan_docs" ]; then
	echo "no-plan-docs: staged diff mixes Go code with planning docs:" >&2
	for f in $plan_docs; do
		echo "  $f" >&2
	done
	echo "CLAUDE.md rule #10: don't ship plan/audit docs alongside implementation." >&2
	echo "Commit the docs separately, or drop them. Bypass once with --no-verify." >&2
	exit 1
fi
