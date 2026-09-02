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

# scan_cs — as scan, but case sensitive. Needed by the leading-word rules below,
# where the whole point is the exact casing: "API" must not also match "api", and
# a lowercase "sign-in" must not also match a capitalised "Sign-in" in prose.
scan_cs() {
	local pattern=$1
	local desc=$2
	shift 2
	[ "$#" -gt 0 ] || return 0
	local m
	m=$(grep -nHE "$pattern" "$@" 2>/dev/null | strip_allowed || true)
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

# Leading word in flag usage and error strings.
#
# fang renders help and errors through titleFirstWord, which runs cases.Title
# over the FIRST word only. That silently mangles anything that must keep its
# own casing: "API key" prints as "Api key", "JSON schema" as "Json schema",
# "per-page" as "Per-Page", and worst of all "--schema and ..." prints as
# "--Schema and ...", which is a flag name the user cannot paste back.
#
# There is no fang option to turn this off, so the rule is: no user-facing
# string may START with a flag name, an acronym, or a hyphenated lowercase word.
# Reword instead ("key for the product API", "schema as JSON", "pass --schema or
# --schema-name, not both"). Mid-string is unaffected and needs no change.
#
# Test files are excluded: their assertion messages are not user facing.
#
# Known blind spot: a format string starting with a verb ("%s needs ...") hides
# whatever gets substituted in, so a message leading with a command path or flag
# still slips through. Prefer wording that puts a literal word first.
ACRONYM='API|JSON|URL|ID|HTTP|HTTPS|CLI|MCP|TOML|SSE|OAuth|PKCE|NDJSON|TTY|XDG'
LEAD="(--|($ACRONYM) |[a-z]+-[a-z]+ )"

tracked all_go '*.go'
src_go=()
for f in ${all_go[@]+"${all_go[@]}"}; do
	case $f in
	*_test.go) ;;
	*) src_go+=("$f") ;;
	esac
done

scan_cs "(errors\.New|fmt\.Errorf)\(\"$LEAD" \
	'Error string starts with a flag name, acronym, or hyphenated word; fang will title-case it (e.g. "--schema" -> "--Schema"). Reword the first word:' \
	${src_go[@]+"${src_go[@]}"}

scan_cs ", \"$LEAD[^\"]*\"\)" \
	'Flag usage string starts with a flag name, acronym, or hyphenated word; fang will title-case it (e.g. "API key" -> "Api key"). Reword the first word:' \
	${src_go[@]+"${src_go[@]}"}

if [ "$fail" -eq 0 ]; then
	echo "lint-copy: clean"
fi

exit $fail
