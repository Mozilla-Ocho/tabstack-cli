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
# Scope: README.md, AGENTS.md, and tracked *.go files. Uses a literal "—" match
# (not grep -P) so it behaves identically on GNU and BSD/macOS grep, and avoids
# bash 4 features (mapfile) so it runs on stock macOS bash 3.2.
#
# To intentionally allow one of these on a specific line, append the marker
#   lint-copy: allow
# in a trailing comment on that line.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0

# strip_allowed drops lines carrying the "lint-copy: allow" escape hatch.
strip_allowed() { grep -v 'lint-copy: allow' || true; }

# scan PATTERN DESCRIPTION FILE... — grep FILEs for PATTERN, print DESCRIPTION and
# the hits (minus allowed lines), and flag failure. No-ops when no files match.
scan() {
	pattern=$1
	desc=$2
	shift 2
	[ "$#" -gt 0 ] || return 0
	m=$(grep -inE "$pattern" "$@" 2>/dev/null | strip_allowed || true)
	if [ -n "$m" ]; then
		echo "$desc"
		echo "$m"
		fail=1
	fi
}

# Em dashes: user-facing docs plus Go source (help/error text; em dashes have no
# business in Go source anyway).
scan '—' 'Em dash (U+2014) found; use a comma, colon, or a sentence split:' \
	$(git ls-files 'README.md' 'AGENTS.md' '*.go')

# Banned term: docs only.
scan 'scrap(e|er|ing)' 'Banned term (scrape/scraper/scraping) in docs; prefer "extract"/"automate":' \
	$(git ls-files '*.md')

if [ "$fail" -eq 0 ]; then
	echo "lint-copy: clean"
fi

exit $fail
