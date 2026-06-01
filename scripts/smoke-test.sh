#!/usr/bin/env bash
#
# smoke-test.sh — exercise every tabstack command against the live API.
#
# Requires a valid API key (TABSTACK_API_KEY env var, --api-key, or a saved
# config file via `tabstack auth login`).
#
# Usage:
#   scripts/smoke-test.sh                  # run everything
#   BIN=./bin/tabstack scripts/smoke-test.sh
#   SKIP_AGENT=1 scripts/smoke-test.sh     # skip the slow/costly agent calls
#   URL=https://example.com QUERY="..." scripts/smoke-test.sh
#
# Notes:
#   - agent automate/research hit AI endpoints; they are slow and cost credits.
#     Set SKIP_AGENT=1 to skip them.
#   - `agent input` needs a request ID from a paused automation, so it cannot be
#     scripted blind; it is exercised only for its usage/validation path.

set -uo pipefail

# --- config ------------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BIN:-$REPO_ROOT/bin/tabstack}"
URL="${URL:-https://example.com}"
QUERY="${QUERY:-What is the capital of France?}"
SKIP_AGENT="${SKIP_AGENT:-0}"

PASS=0
FAIL=0
FAILED=()

# Temp dir for generated schema/instruction fixtures.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- helpers -----------------------------------------------------------------

c_blue=$'\033[34m'; c_green=$'\033[32m'; c_red=$'\033[31m'; c_dim=$'\033[2m'; c_off=$'\033[0m'

# run NAME -- CMD ARGS...   (everything after `--` is passed to the binary)
run() {
  local name="$1"; shift
  [ "$1" = "--" ] && shift

  printf '%s▶ %s%s\n' "$c_blue" "$name" "$c_off"
  printf '%s  $ %s %s%s\n' "$c_dim" "$BIN" "$*" "$c_off"

  if "$BIN" "$@"; then
    printf '%s  ✅ PASS%s\n\n' "$c_green" "$c_off"
    PASS=$((PASS + 1))
  else
    local rc=$?
    printf '%s  ❌ FAIL (exit %d)%s\n\n' "$c_red" "$rc" "$c_off"
    FAIL=$((FAIL + 1))
    FAILED+=("$name")
  fi
}

# run_expect_fail NAME EXPECTED_RC -- CMD ARGS...
# Passes when the command exits with EXPECTED_RC (for negative/validation cases).
run_expect_fail() {
  local name="$1" want="$2"; shift 2
  [ "$1" = "--" ] && shift

  printf '%s▶ %s%s\n' "$c_blue" "$name" "$c_off"
  printf '%s  $ %s %s  (expect exit %s)%s\n' "$c_dim" "$BIN" "$*" "$want" "$c_off"

  "$BIN" "$@" >/dev/null 2>&1
  local rc=$?
  if [ "$rc" -eq "$want" ]; then
    printf '%s  ✅ PASS (exit %d)%s\n\n' "$c_green" "$rc" "$c_off"
    PASS=$((PASS + 1))
  else
    printf '%s  ❌ FAIL (exit %d, wanted %d)%s\n\n' "$c_red" "$rc" "$want" "$c_off"
    FAIL=$((FAIL + 1))
    FAILED+=("$name")
  fi
}

# --- preflight ---------------------------------------------------------------

if [ ! -x "$BIN" ]; then
  printf 'Building %s ...\n' "$BIN"
  ( cd "$REPO_ROOT" && make build ) || { echo "build failed"; exit 1; }
fi

# Confirm a key resolves before spending time/credits.
if ! "$BIN" auth status --no-color 2>&1 | grep -q "API key configured"; then
  printf '%sNo API key configured.%s Set TABSTACK_API_KEY, pass --api-key, or run `tabstack auth login`.\n' "$c_red" "$c_off"
  exit 1
fi

# Fixtures.
cat > "$TMP/schema.json" <<'JSON'
{
  "type": "object",
  "properties": {
    "title": { "type": "string", "description": "The page title" }
  },
  "required": ["title"]
}
JSON

printf '%s=== tabstack smoke test ===%s\n' "$c_blue" "$c_off"
printf 'binary: %s\nurl:    %s\n\n' "$BIN" "$URL"

# --- auth --------------------------------------------------------------------

run "auth status" -- auth status

# --- extract -----------------------------------------------------------------

run "extract markdown" -- extract markdown "$URL" --metadata
run "extract markdown (json output)" -- extract markdown "$URL" --output json
run "extract json (schema @file)" -- extract json "$URL" --schema "@$TMP/schema.json"
run "extract json (schema literal)" -- extract json "$URL" \
  --schema '{"type":"object","properties":{"title":{"type":"string"}}}'

# --- generate ----------------------------------------------------------------

run "generate json" -- generate json "$URL" \
  --instructions "Extract the page title." \
  --schema "@$TMP/schema.json"

# --- validation / negative cases (no API spend) ------------------------------

# Missing required flag -> cobra usage error (exit 1).
run_expect_fail "extract json missing --schema" 1 -- extract json "$URL"
# Flag present but empty -> app validation (exit 2).
run_expect_fail "generate json empty --instructions" 2 -- generate json "$URL" --schema '{}' --instructions ''
# Malformed JSON caught locally before any API call (exit 2).
run_expect_fail "extract json invalid JSON schema" 2 -- extract json "$URL" --schema 'not json'
run_expect_fail "agent input missing --data" 1 -- agent input some-request-id

# --- agent (slow / costly) ---------------------------------------------------

if [ "$SKIP_AGENT" = "1" ]; then
  printf '%sSkipping agent automate/research (SKIP_AGENT=1)%s\n\n' "$c_dim" "$c_off"
else
  run "agent research (fast)" -- agent research "$QUERY" --mode fast
  run "agent automate" -- agent automate "Get the page heading" --url "$URL" --max-iterations 3
fi

# --- summary -----------------------------------------------------------------

printf '%s=== summary ===%s\n' "$c_blue" "$c_off"
printf 'passed: %d   failed: %d\n' "$PASS" "$FAIL"
if [ "$FAIL" -gt 0 ]; then
  printf '%sfailed cases:%s\n' "$c_red" "$c_off"
  for n in "${FAILED[@]}"; do printf '  - %s\n' "$n"; done
  exit 1
fi
printf '%sall passed%s\n' "$c_green" "$c_off"
