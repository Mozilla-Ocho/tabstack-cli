#!/usr/bin/env bash
#
# smoke-mcp.sh: exercise the `tabstack mcp` stdio server end to end.
#
# Speaks JSON-RPC over the server's stdin/stdout: initialize, then tools/list,
# and asserts the handshake succeeds and every expected tool is registered. The
# handshake and tools/list make no product call, so this runs offline with a
# placeholder key. If a real key is available (TABSTACK_API_KEY, or set
# LIVE=1 with a configured key), it also calls extract_markdown against $URL.
#
# Usage:
#   scripts/smoke-mcp.sh
#   BIN=./bin/tabstack scripts/smoke-mcp.sh
#   LIVE=1 URL=https://example.com scripts/smoke-mcp.sh   # also call a tool live
#
# The server is driven through a pair of FIFOs so reads block until it replies
# (no sleeps, no all-at-once EOF race that would drop responses). FIFOs, a
# background job, and read -t are all portable to stock macOS bash 3.2, unlike
# coproc (bash 4+).

set -uo pipefail

# --- config ------------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BIN:-$REPO_ROOT/bin/tabstack}"
URL="${URL:-https://example.com}"
LIVE="${LIVE:-0}"

# The server needs a key to *resolve* before it will serve, but the protocol
# handshake and tools/list never send it anywhere, so a placeholder is fine.
PLACEHOLDER_KEY="smoke-mcp-placeholder"

PASS=0
FAIL=0
FAILED=()

c_blue=$'\033[34m'; c_green=$'\033[32m'; c_red=$'\033[31m'; c_dim=$'\033[2m'; c_off=$'\033[0m'

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- helpers -----------------------------------------------------------------

# check NAME HAYSTACK NEEDLE -- pass when NEEDLE is a substring of HAYSTACK.
check() {
  local name="$1" hay="$2" needle="$3"
  printf '%s▶ %s%s\n' "$c_blue" "$name" "$c_off"
  if printf '%s' "$hay" | grep -qF -- "$needle"; then
    printf '%s  ✅ PASS%s\n' "$c_green" "$c_off"
    PASS=$((PASS + 1))
  else
    printf '%s  ❌ FAIL (missing %q)%s\n' "$c_red" "$needle" "$c_off"
    FAIL=$((FAIL + 1))
    FAILED+=("$name")
  fi
}

# check_not NAME HAYSTACK NEEDLE -- pass when NEEDLE is NOT in HAYSTACK.
check_not() {
  local name="$1" hay="$2" needle="$3"
  printf '%s▶ %s%s\n' "$c_blue" "$name" "$c_off"
  if printf '%s' "$hay" | grep -qF -- "$needle"; then
    printf '%s  ❌ FAIL (unexpected %q)%s\n' "$c_red" "$needle" "$c_off"
    FAIL=$((FAIL + 1))
    FAILED+=("$name")
  else
    printf '%s  ✅ PASS%s\n' "$c_green" "$c_off"
    PASS=$((PASS + 1))
  fi
}

# --- preflight ---------------------------------------------------------------

if [ ! -x "$BIN" ]; then
  printf 'Building %s ...\n' "$BIN"
  ( cd "$REPO_ROOT" && make build ) || { echo "build failed"; exit 1; }
fi

# A real key (env, or config when LIVE=1) enables the optional live tool call.
RUN_KEY="$PLACEHOLDER_KEY"
DO_LIVE=0
if [ -n "${TABSTACK_API_KEY:-}" ]; then
  RUN_KEY="$TABSTACK_API_KEY"
  DO_LIVE=1
elif [ "$LIVE" = "1" ]; then
  # Rely on a configured key/session; leave the env unset so the server resolves
  # it from config.
  RUN_KEY=""
  DO_LIVE=1
fi

printf '%s=== tabstack mcp smoke test ===%s\n' "$c_blue" "$c_off"
printf 'binary: %s\nlive:   %s\n\n' "$BIN" "$DO_LIVE"

# --- drive the server --------------------------------------------------------

REQ_INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
REQ_INITED='{"jsonrpc":"2.0","method":"notifications/initialized"}'
REQ_TOOLS='{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
REQ_CALL='{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"extract_markdown","arguments":{"url":"'"$URL"'"}}}'

RESP_INIT=""; RESP_TOOLS=""; RESP_CALL=""; SRV_RC=0

# Two FIFOs: IN feeds the server's stdin, OUT reads its stdout.
IN="$TMP/in"; OUT="$TMP/out"
mkfifo "$IN" "$OUT"

if [ -n "$RUN_KEY" ]; then
  TABSTACK_API_KEY="$RUN_KEY" "$BIN" mcp <"$IN" >"$OUT" 2>"$TMP/err" &
else
  "$BIN" mcp <"$IN" >"$OUT" 2>"$TMP/err" &
fi
SRV_PID=$!

# Hold the write end of IN open (fd 3) so the server's stdin does not hit EOF
# until we are done, and open OUT for reading (fd 4).
exec 3>"$IN"
exec 4<"$OUT"

printf '%s\n' "$REQ_INIT" >&3
if ! IFS= read -r -t 20 RESP_INIT <&4; then
  printf '%sserver did not respond to initialize%s\n' "$c_red" "$c_off"
  printf '%s--- server stderr ---%s\n' "$c_dim" "$c_off"; cat "$TMP/err" || true
  exec 3>&- 4<&-; wait "$SRV_PID" 2>/dev/null
  exit 1
fi

printf '%s\n' "$REQ_INITED" >&3
printf '%s\n' "$REQ_TOOLS" >&3
IFS= read -r -t 20 RESP_TOOLS <&4 || true

if [ "$DO_LIVE" = "1" ]; then
  printf '%s\n' "$REQ_CALL" >&3
  IFS= read -r -t 120 RESP_CALL <&4 || true
fi

# Close our stdin write end so the server sees EOF and shuts down; collect its
# exit code, then release the read end.
exec 3>&-
wait "$SRV_PID"; SRV_RC=$?
exec 4<&-

# --- assertions --------------------------------------------------------------

check "initialize returns serverInfo" "$RESP_INIT" '"serverInfo"'
check "initialize identifies as tabstack" "$RESP_INIT" 'tabstack'

for t in extract_markdown extract_json generate_json automate research \
         schema_list schema_resolve whoami list_orgs active_org; do
  check "tools/list has $t" "$RESP_TOOLS" "$t"
done

if [ "$DO_LIVE" = "1" ]; then
  check "extract_markdown returns a result" "$RESP_CALL" '"result"'
  check_not "extract_markdown is not an error" "$RESP_CALL" '"isError":true'
else
  printf '%sSkipping live tool call (no API key; set TABSTACK_API_KEY or LIVE=1)%s\n' "$c_dim" "$c_off"
fi

printf '%s▶ clean shutdown on stdin close%s\n' "$c_blue" "$c_off"
if [ "$SRV_RC" -eq 0 ]; then
  printf '%s  ✅ PASS (exit 0)%s\n' "$c_green" "$c_off"; PASS=$((PASS + 1))
else
  printf '%s  ❌ FAIL (exit %d)%s\n' "$c_red" "$SRV_RC" "$c_off"; FAIL=$((FAIL + 1)); FAILED+=("clean shutdown")
fi

# --- summary -----------------------------------------------------------------

printf '\n%s=== summary ===%s\n' "$c_blue" "$c_off"
printf 'passed: %d   failed: %d\n' "$PASS" "$FAIL"
if [ "$FAIL" -gt 0 ]; then
  printf '%sfailed cases:%s\n' "$c_red" "$c_off"
  for n in "${FAILED[@]}"; do printf '  - %s\n' "$n"; done
  exit 1
fi
printf '%sall passed%s\n' "$c_green" "$c_off"
