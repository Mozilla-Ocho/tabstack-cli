#!/usr/bin/env bash
#
# lint-copy.sh — enforce Tabstack voice rules that are mechanical enough to catch
# automatically, so they never reach human review:
#
#   1. No em dash (U+2014, "—") in user-facing docs or Go source. Use a comma,
#      colon, or a sentence split instead. En dashes in numeric ranges like
#      "1-5s" use a plain hyphen and are not affected.
#   2. No "scrape"/"scraper"/"scraping" in docs. Tabstack does structured
#      extraction and browser automation; the scraper framing is a third-party
#      SEO surface this repo is not.
#
# Scope: README.md, AGENTS.md, and tracked *.go files. AGENTS.md is linted but
# CLAUDE.md is not: CLAUDE.md carries machine-facing instructions with intentional
# em dashes, whereas AGENTS.md is prose held to the same voice as the docs.
#
# Uses a literal "—" match (not grep -P) so it behaves identically on GNU and
# BSD/macOS grep, and avoids bash 4 features (mapfile) so it runs on stock macOS
# bash 3.2.
#
# To intentionally allow one of these on a specific line, append the marker
#   lint-copy: allow
# inside a trailing comment on that line: "//" for Go, "<!-- ... -->" for
# Markdown. The marker only counts inside a comment leader, so prose that merely
# mentions the marker cannot silently exempt itself.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0

# strip_allowed drops lines carrying the "lint-copy: allow" escape hatch, but
# only when the marker sits inside a comment leader ("//" or "<!--"), so a prose
# line that merely names the marker is still linted.
strip_allowed() { grep -vE '(//|<!--).*lint-copy: allow' || true; }

# scan PATTERN DESCRIPTION FILE... — grep FILEs for PATTERN, print DESCRIPTION and
# the hits (minus allowed lines), and flag failure. No-ops when no files match.
scan() {
	local pattern=$1
	local desc=$2
	shift 2
	[ "$#" -gt 0 ] || return 0
	local m
	m=$(grep -inHE "$pattern" "$@" 2>/dev/null | strip_allowed || true)
	if [ -n "$m" ]; then
		echo "$desc"
		echo "$m"
		fail=1
	fi
}

# tracked FILES <- git ls-files GLOB... — collect NUL-delimited tracked paths into
# the FILES array, so paths with spaces or glob chars survive intact (no word
# splitting on an unquoted command substitution).
tracked() {
	local name=$1
	shift
	local f
	eval "$name=()"
	while IFS= read -r -d '' f; do
		eval "$name+=(\"\$f\")"
	done < <(git ls-files -z "$@")
}

# Em dashes: user-facing docs plus Go source (help/error text; em dashes have no
# business in Go source anyway).
tracked em_files 'README.md' 'AGENTS.md' '*.go'
scan '—' 'Em dash (U+2014) found; use a comma, colon, or a sentence split:' \
	${em_files[@]+"${em_files[@]}"}

# Banned term: docs only.
tracked md_files '*.md'
scan 'scrap(e|er|ing)' 'Banned term (scrape/scraper/scraping) in docs; prefer "extract"/"automate":' \
	${md_files[@]+"${md_files[@]}"}

if [ "$fail" -eq 0 ]; then
	echo "lint-copy: clean"
fi

exit $fail
