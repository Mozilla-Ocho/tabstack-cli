#!/usr/bin/env bash
#
# lint-copy.sh — enforce Tabstack voice rules that are mechanical enough to catch
# automatically, so they never reach human review:
#
#   1. No em dash (U+2014, "—") or horizontal bar (U+2015, "―") in user-facing
#      docs or Go source. Use a comma, colon, or a sentence split instead. The en
#      dash (U+2013, "–") is deliberately left alone: it is the correct character
#      for the numeric ranges the docs already use ("~1–5s", "1–100").
#   2. No "scrape"/"scraper"/"scraping" in docs or Go source. Tabstack does
#      structured extraction and browser automation; the scraper framing is a
#      third-party SEO surface this repo is not.
#
# Scope, by rule:
#   dashes:      *README.md, *AGENTS.md, *.go
#   banned term: *.md (CLAUDE.md included), *.go
#
# CLAUDE.md is exempt from the dash rule only: it carries machine-facing
# instructions with intentional em dashes, whereas the other docs are prose held
# to the voice rules. openapi.yaml is out of scope entirely: it is an export of
# the server's spec (its prose cites server-side source files), so local edits
# would be overwritten on the next re-export.
#
# Untracked-but-not-ignored files are scanned too (`git grep --untracked`), so a
# brand new file fails here rather than after `git add` in CI.
#
# Uses `git grep` for both the file selection and the match, so pathspecs, NUL
# safety, and error reporting are git's problem, not the shell's. git grep exits
# 1 for "no match" and >1 for a real failure, which is what makes this gate fail
# closed: any git-level error (not a repo, dubious ownership, missing git) exits
# 2 instead of silently reporting clean.
#
# To intentionally allow one of these on a specific line, append the marker
#   lint-copy: allow
# inside a comment that is real for that file type: "//" for Go, "<!-- ... -->"
# for Markdown. A "//" that is part of a URL ("https://") does not count, and a
# Markdown comment does not exempt a Go line (or the reverse), so prose that
# merely mentions the marker cannot silently exempt itself.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0

# strip_allowed drops "path:line:content" records carrying the "lint-copy: allow"
# escape hatch, honouring only the comment syntax that is real for that path's
# file type.
strip_allowed() {
	awk '
	{
		p = index($0, ":")
		path = substr($0, 1, p - 1)
		rest = substr($0, p + 1)
		q = index(rest, ":")
		content = substr(rest, q + 1)

		allow = 0
		if (path ~ /\.go$/)
			allow = (content ~ /(^|[^:])\/\/.*lint-copy: allow/)
		else if (path ~ /\.md$/)
			allow = (content ~ /<!--.*lint-copy: allow.*-->/)

		if (!allow)
			print
	}'
}

# scan PATTERN DESCRIPTION PATHSPEC... — search the pathspecs for PATTERN, print
# DESCRIPTION and the hits (minus allowed lines), and flag failure. Any git-level
# error aborts with exit 2 rather than passing.
scan() {
	local pattern=$1
	local desc=$2
	shift 2
	local out status
	if out=$(git grep --no-color --untracked -I -inE -e "$pattern" -- "$@"); then
		status=0
	else
		status=$?
	fi
	if [ "$status" -gt 1 ]; then
		echo "lint-copy: git grep failed (exit $status) while scanning for: $desc" >&2
		exit 2
	fi
	[ -n "$out" ] || return 0
	out=$(printf '%s\n' "$out" | strip_allowed)
	if [ -n "$out" ]; then
		echo "$desc"
		echo "$out"
		fail=1
	fi
}

# Em dash and horizontal bar: user-facing docs plus Go source (help/error text;
# neither has any business in Go source anyway).
scan '—|―' 'Em dash (U+2014) or horizontal bar (U+2015) found; use a comma, colon, or a sentence split:' \
	'*README.md' '*AGENTS.md' '*.go'

# Banned term: docs plus Go source (command help and error strings are copy too).
scan 'scrap(e|er|ing)' 'Banned term (scrape/scraper/scraping); prefer "extract"/"automate":' \
	'*.md' '*.go'

if [ "$fail" -eq 0 ]; then
	echo "lint-copy: clean"
fi

exit $fail
