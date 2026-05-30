#!/bin/sh
# check-commit-msg.sh — enforce HOW_TO_COMMIT.md conventions.
# Invoked by the commit-msg lefthook with the path to the commit message file.
#
#   First line: <= 50 chars, imperative mood.
#   Blank line separates subject from body.
#   Body explains *why*.

set -e

msg_file="$1"
if [ -z "$msg_file" ] || [ ! -f "$msg_file" ]; then
    echo "check-commit-msg: no commit message file passed" >&2
    exit 1
fi

# First non-comment line is the subject; git strips '#' comment lines later,
# so ignore them here too.
subject=$(grep -v '^#' "$msg_file" | sed '/./,$!d' | head -n 1)

# Skip generated/auto messages we don't author by hand: merges, reverts,
# fixup!/squash! autosquash commits, and empty messages (git aborts those).
case "$subject" in
    "Merge "*|"Revert "*|"fixup! "*|"squash! "*|"") exit 0 ;;
esac

# Blocking rule: subject <= 50 chars (HOW_TO_COMMIT.md). Unambiguous, cheap.
if [ "${#subject}" -gt 50 ]; then
    echo "FAIL: subject is ${#subject} chars (max 50): $subject" >&2
    echo "Tighten the subject; move detail into the body. See HOW_TO_COMMIT.md." >&2
    exit 1
fi

# Blocking rule: no trailing period on the subject (git/imperative convention).
case "$subject" in
    *.) echo "FAIL: subject must not end with a period: $subject" >&2; exit 1 ;;
esac

# Non-blocking heuristic: imperative mood. Past-tense/gerund openers are a
# common slip, but detection misfires, so we warn rather than block — keeping
# people off the --no-verify habit.
first_word=$(printf '%s' "$subject" | awk '{print $1}')
case "$first_word" in
    Added|Fixed|Updated|Removed|Changed|Adding|Fixing|Updating|Removing|Changing)
        echo "warning: subject opens with '$first_word' — prefer imperative mood (Add/Fix/Update/...)." >&2
        ;;
esac


# Body check: if a second line exists, it must be blank (subject/body
# separation). This part is fixed policy, not up for decision.
second_line=$(grep -v '^#' "$msg_file" | sed '/./,$!d' | sed -n '2p')
if [ -n "$second_line" ]; then
    echo "FAIL: line 2 of the commit message must be blank (separate subject from body)." >&2
    echo "See HOW_TO_COMMIT.md." >&2
    exit 1
fi

exit 0
