# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`tabstack` is a Go CLI client for the Tabstack AI API: browser automation, web research, and structured extraction/generation from any URL. It is a hand-written HTTP client (no generated code) wrapped in a Cobra command tree.

## Commands

```bash
make build       # build into ./bin/tabstack (version stamped from `git describe`)
make install     # install into $GOPATH/bin
make run ARGS="extract markdown --url ..."
make lint        # gofmt -w . then go vet ./...
make test        # go test ./...
make help        # list all targets
```

Version is injected via `-ldflags -X .../cmd.version=$(VERSION)`; `VERSION` comes from `git describe --tags --always --dirty`, falling back to `dev`.

Tests live alongside each package (`*_test.go`); GitHub Actions runs gofmt/vet/build/test on push and PRs (`.github/workflows/ci.yml`). `client.WithHTTPClient` injects a mock `*http.Client` for tests. `scripts/smoke-test.sh` (`make smoke`) exercises every command against the live API.

## Architecture

Three layers, each in its own package:

- **`cmd/tabstack/main.go`**: entry point; builds the root command and runs it through `fang.Execute` (charmbracelet/fang), which renders styled help, errors, version output, and adds `completion`/`man` subcommands. fang prints errors itself and returns them, so main keeps owning the exit-code mapping: it checks for any error implementing `Code() int` (the `coded` interface) and exits with that code, else maps cobra usage errors to 2, else 1. Version is handed to fang via `cmd.Version()` (stamped by `-ldflags`). (Lives in its own dir so `go install .../cmd/tabstack` produces a `tabstack` binary.)
- **`cmd/`**: Cobra command tree, one file per endpoint group (`agent`, `extract`, `generate`, `schema`, `auth`, `keys`). Each leaf command only builds its request and calls the client. The auth flow is split across `auth.go` (status/logout/sessions), `login.go` (the OAuth flow and loopback callback), `switch.go` (org switching), `keysetup.go` (the shared key-setup prompt), and `orgs.go` (org selector resolution).
- **`internal/client/`**: the HTTP client and per-endpoint request/response types for the **product** host. Sends the org-scoped API key, never the session.
- **`internal/console/`**: the client for the **auth and management** host (`console.tabstack.ai`): `/oauth/*` (PKCE authorize URL, code exchange, refresh) and `/cli/*` (me, organizations, api_keys, sessions, logout). Sends the user-scoped session token, never an API key. Split by host on purpose so the wrong credential cannot reach the wrong endpoint.
- **`internal/schemas/`**: fetches the public `tabstack-schemas` GitHub repo and manages the local schema store (`schema pull`/`schema list`). Kept separate from `client`: it talks to `raw.githubusercontent.com` unauthenticated, so it must never see the API bearer token. Mirrors `client`'s injectable-`http.Client` pattern (`schemas.WithHTTPClient`) for tests.
- **`internal/config/`**: credential and base-URL resolution. Also resolves `SchemasDir()` (the default `schema pull` destination, alongside the config file under the tabstack config home).
- **`internal/ui/`**: output rendering (pretty vs JSON), styles, spinner.

### Shared app context

`cmd/root.go` defines `app{store, cfg, key, client, renderer, orgOverride}` and stores it in the package-global `rootApp`. The root command's `PersistentPreRunE` populates it **once** before any subcommand runs, so leaf commands never re-resolve config or rebuild the client. Commands tagged with the `skipClient` annotation (all of `auth`, `keys`, and `schema`) get a config-and-renderer setup with no product client, so they work before an API key exists. `consoleClient()`/`requireSession()` build the auth-host client with the session attached.

### Credentials, orgs, and resolution priority

Two credentials, never conflated: a **session** (`Config.Session`, OAuth access + refresh token, user scoped, auth host only) and **API keys** (`Config.Orgs[orgID].APIKey`, org scoped, product host only). In code they are always `SessionToken`-ish and `APIKey`-ish, never a bare `Token`/`Key`.

`Config` is real TOML (`github.com/pelletier/go-toml/v2`) at `$XDG_CONFIG_HOME/tabstack/config.toml`, version 1. **Struct field order matters**: TOML tables (`session`, `orgs`) must come after the scalars, so they are declared last. `Orgs` is keyed by organisation **id** — names are display only and can change.

All reads and writes go through `config.CredentialStore` (`Load`/`Save`/`Path`) so an OS keychain implementation can be added without touching command code; `FileStore` is the only implementation. Saves are atomic (temp file in the same dir → chmod 0600 → rename), the parent dir is forced to 0700, and a group/world-readable file warns on load without refusing to run.

**Migration**: a file with no `version` and a top-level `api_key` has that value moved to `LegacyAPIKey` in memory on load. Nothing is rewritten until the next successful `Save`, and the old key is never deleted. `LegacyAPIKey` is only used while `ActiveOrg` is empty. There is deliberately no `config migrate` command — migration is automatic and lossless, so an explicit verb would be a no-op nobody knows when to run. `cmd/config.go` covers what migration actually leaves behind: `config show` (whole file, every org, secrets redacted via `config.Redact` with no flag to print them in full), `config path` (scriptable), and `config drop-legacy-key`, which refuses unless the active org has a key of its own so it cannot strand a user without a working credential (`--force` overrides).

`Config.ResolveAPIKey` is the single product-credential resolver every command shares: **`--api-key`/`--key`** → **`TABSTACK_API_KEY`** → **stored key for the `--org` override** → **stored key for `ActiveOrg`** → **`LegacyAPIKey` (only with no active org)** → error. A `--org` override with no stored key is an error naming the exact `keys create` command, never a fallback to another org's key. `KeySource`/`EnvOverriding` are tracked so `auth status` can explain the resolution without printing the key. URLs resolve separately (`ResolveBaseURL`, `ResolveAuthURL`): file → env → flag.

`--org` is a one-shot override: it resolves against the **local** config only (product commands never make a management call to pick a key), never mutates `ActiveOrg`, and prints the acting org to stderr so it lands in logs rather than piped stdout.

### OAuth login and org switching

`cmd/login.go` runs authorization code + PKCE (S256 only, `plain` is not implemented): 32-byte `crypto/rand` verifier and state, a listener on `127.0.0.1:0` (literal, never `localhost`) whose port builds the redirect URI, a one-shot handler that 404s every path but `/callback`, `subtle.ConstantTimeCompare` on state, RFC 9207 `iss` check, and a 2-minute timeout. The callback page sets `Referrer-Policy: no-referrer` and contains neither code nor state, is explicitly `Flush`ed, and the loopback server is torn down with `Shutdown` (not `Close`) so the page reaches the browser instead of a reset on the fast failure path. On success the page redirects the browser to the console (the configured auth host) via a `meta refresh` with a manual-link fallback — never a value from the callback query, so nothing leaks onward — rather than leaving it parked on the loopback URL; the failure page stays put so the error is readable. `openBrowser`/`hasDisplay` are package vars so the whole flow is testable end to end against `httptest` with no browser. `ExchangeCode` sends a `label` (the hostname) so each session is legible in `auth sessions` rather than showing up as the Go User-Agent.

`console.SessionManager` owns the live session: it hands out a valid access token, refreshes when expired (**single-flight** — the server rotates the refresh token, so two concurrent refreshes would leave one holding a dead value), persists the rotation, and reads `expires_in` off every response into an absolute `ExpiresAt` (the lifetime is never hardcoded). A 401 from `/cli/*` is read for its error code: `session_expired` (an aged-out access token, or a 401 with no recognised code) refreshes **once** and retries **once**, then fails with `ErrSessionExpired`; `invalid_session` (unknown/revoked/wrong-audience token) is terminal immediately with `ErrInvalidSession` and **no** refresh, since handing the server the same identity cannot help. It never loops. Single-flight only guards *within* a process; across processes (the MCP server and the CLI share the stored session), an `invalid_grant` on refresh triggers a re-read of the store — if the persisted refresh token changed, a sibling already rotated, so the manager adopts that session (or retries once with its token) instead of forcing a needless re-login.

Org switching (`cmd/switch.go`) is a credential change, not a display setting. Selector resolution (`cmd/orgs.go`) is exact id → exact case-insensitive name → unique case-insensitive prefix, with ambiguity and unknown values listing candidates and failing rather than guessing. A single-org user is told so instead of being shown a picker. If `/cli/organizations` is unreachable and the argument matches an org already in config, the switch proceeds with a warning; an org list the server actively rejected is a hard error.

`console.RevealEnabled` gates the `/cli/api_keys/:id/reveal` endpoint and the "use existing key" login option. Both are part of the auth contract, so it is on; the constant remains the single switch that would remove the whole reveal path cleanly if the product ever pulls it. Even when on, login only offers "use existing" once it has listed the org's keys and found at least one to adopt, and `--api-key-setup=existing` errors when the org has none; a single candidate is adopted without prompting. The same adoption is available on demand as `keys use [key-id]` (`cmd/keys.go`), which unlike the login path also replaces an already-stored key; both share `cmd/keysetup.go`'s `adoptKey`/`chooseKey` (id-match, or sole-candidate/prompt selection). `console.DefaultScopes` is provisional and overridable with `TABSTACK_OAUTH_SCOPES`.

### Two request styles

The client splits on transport, not endpoint:

- **`doJSON`**: single JSON request/response. Used by `extract/json`, `extract/markdown`, `generate/json`, `automate/{id}/input`. Schema-driven endpoints (`extract/json`, `generate/json`) return `json.RawMessage` verbatim because the response shape is caller-defined by the supplied JSON schema.
- **`doStream`**: Server-Sent Events. Used by `automate` and `research`. Deliberately imposes **no** client timeout (a hard timeout would cut the stream); cancellation flows through `context`. `--timeout` only affects non-streaming calls.

`internal/client/sse.go` (`ParseSSE`) is a from-scratch SSE parser with a 4MB scanner buffer (extracted page content exceeds the default 64KB token limit).

### Schema pull (third transport)

`schema pull` fetches pre-defined extraction schemas from the public `tabstack-schemas` repo into a local store, so they can be fed to `extract json`. `index.json` at the repo root is the manifest; `schemas.Index.Resolve` maps a selector (bare name, category, or full repo path) onto entries. The on-disk layout mirrors the repo (`<store>/jobs/job-posting.json`) for stable identity. On re-pull, `schemas.Equal` compares canonicalised JSON (key-order/whitespace-insensitive) so only real content drift counts as a conflict; conflicts prompt overwrite/keep/quit on a TTY, and on a non-TTY (or with a customised file and no `--force`) fail with exit 2 rather than silently clobbering local edits. Schema files are written 0644/0755 (not secrets, unlike the 0600 `config.toml`).

`extract json` and `generate json` accept `--schema-name <name>` to consume a pulled schema instead of inline `--schema`. `cmd/helpers.go`'s `resolveSchemaArg` is the shared resolver for both (mutual-exclusion check + inline-vs-store resolution); `schemas.FindLocal` is the offline counterpart to `Index.Resolve`, resolving the selector by scanning the local store (never the network). Both flags share the `--storage` override.

Pull records provenance in `<store>/.manifest.json` (`schemas.Manifest`): the canonical SHA-256 (`CanonicalSHA`, formatting-insensitive) of each schema as pulled. `schema status` reads it to classify each schema — `modified` (local file's canonical hash ≠ recorded), `outdated` (current remote's hash ≠ recorded; skipped with `--local`), `missing`, `untracked`. `schema rm` deletes files and their manifest entries; `schema path` prints a stored schema's local path for scripting.

The library index is cached per store in `<store>/.index-cache.json` (`schemas.CachedIndex`, 1h TTL, `--refresh` to bypass, stale-cache fallback when offline). Both bookkeeping dotfiles are skipped by `ListLocal`. Shell completion for `pull` selectors (from the cached index) and `--schema-name`/`rm`/`path` (from the local store) lives in `cmd/schema.go` and honours a typed `--storage`.

### Streaming outcome handling

Streaming endpoints signal failure **in-band** (a `task:completed`/`complete` event with `success:false`, or an `error` event) rather than via HTTP status. `cmd/agent.go`'s `runStream` watches events to capture the final answer and failure state, then the command maps that onto an exit code. The spinner only animates in pretty mode on a real TTY (`os.Stderr`).

### Local MCP server

`tabstack mcp` (`cmd/mcp.go` + `internal/mcp/`) runs a local Model Context Protocol server over **stdio**, exposing the product API as tools an MCP client (Claude Desktop, an IDE) can call. Built on the official `github.com/modelcontextprotocol/go-sdk`; the server is a thin adapter, each tool builds a request and calls the existing `internal/client` (product host) or `internal/console` (auth host) method.

Tools: `extract_markdown`, `extract_json`, `generate_json` (request/response); `automate`, `research` (SSE — each event is forwarded as an MCP progress notification when the client sends a progress token, and the final answer plus in-band success/failure is aggregated into the result); `schema_list`/`schema_resolve` (read-only against the local store, never the network — `schema pull` stays a CLI operation); `whoami`/`list_orgs`/`active_org` (read-only, session-backed via the console client). A tool's returned `error` becomes an MCP tool error (`isError`), never a process exit, so one bad call cannot kill the server.

**stdio invariant**: stdout carries JSON-RPC frames only. `internal/mcp` must never write to stdout; all diagnostics go to stderr, and there is no interactive prompt. The command carries the `skipClient` annotation so the pre-run builds no product client and does not require a key up front. `cmd/mcp.go`'s `resolveMCPKey` resolves the product key like every other command but tolerates a missing one: with a session and an active org it mints a key non-interactively (`CreateAPIKey`) and stores it, so the next start skips that; with no key and no session it exits 2 with `auth login` guidance. Management tools use the session (auto-refresh + rotation); product tools use the resolved API key — the host/credential split is preserved. A closed stdin or a signal is a clean shutdown (`isCleanShutdown`), not a failure exit.

### Exit codes (scriptability)

`cmd/helpers.go` defines `exitErr{code, err}` and `classifyError`:
- **1**: runtime/network error
- **2**: usage error (cobra default; also `withCode(2, ...)` for local input validation)
- **3**: API error (`client.APIError`) or in-band stream failure

`cmd/auth.go`'s `classifyConsoleError` is the auth-host counterpart: an expired or missing session is a configuration problem (2), a rejected management request is an API error (`console.APIError`, 3), anything else falls through to `classifyError`.

### Output modes

`resolveMode` (root.go): `--output pretty|json` wins; otherwise **pretty on a TTY, JSON when piped**, so `tabstack ... | jq` works without a flag. JSON streams emit NDJSON (one line per event). The renderer never reshapes caller-defined schema results.

### Input ergonomics

`readInput`/`readJSON` (helpers.go) accept a literal string, `@file`, or `-` for stdin, mirroring curl's `-d`. `readJSON` validates JSON locally so a malformed schema fails with a clear message instead of an opaque API 400.

## Conventions

- New endpoints: add a request type + method in `internal/client/`, then a leaf command in `cmd/`. Reuse `geoTarget()`, `readJSON()`, and the shared `effort`/`geo`/`nocache` flag names.
- `GeoTarget` and `Effort` (`min`/`standard`/`max`) are shared across fetch-based endpoints.
- Validate caller input locally where the server has known limits (e.g. `maxInstructionsLen = 20000` in `cmd/generate.go`).
