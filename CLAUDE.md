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
- **`cmd/`**: Cobra command tree, one file per endpoint group (`agent`, `extract`, `generate`, `schema`, `auth`). Each leaf command only builds its request and calls the client.
- **`internal/client/`**: the HTTP client and per-endpoint request/response types.
- **`internal/schemas/`**: fetches the public `tabstack-schemas` GitHub repo and manages the local schema store (`schema pull`/`schema list`). Kept separate from `client`: it talks to `raw.githubusercontent.com` unauthenticated, so it must never see the API bearer token. Mirrors `client`'s injectable-`http.Client` pattern (`schemas.WithHTTPClient`) for tests.
- **`internal/config/`**: credential and base-URL resolution. Also resolves `SchemasDir()` (the default `schema pull` destination, alongside the config file under the tabstack config home).
- **`internal/ui/`**: output rendering (pretty vs JSON), styles, spinner.

### Shared app context

`cmd/root.go` defines `app{cfg, client, renderer}` and stores it in the package-global `rootApp`. The root command's `PersistentPreRunE` populates it **once** before any subcommand runs, so leaf commands never re-resolve config or rebuild the client. Commands tagged with the `skipClient` annotation (`auth login`, `auth status`) get a renderer-only setup so they work before an API key exists.

### Config resolution priority

`config.Resolve` merges three sources, lowest to highest precedence: **config file** → **environment** (`TABSTACK_API_KEY`, `TABSTACK_BASE_URL`) → **flags** (`--api-key`, `--base-url`). The config file lives at `$XDG_CONFIG_HOME/tabstack/config.toml` (falls back to `~/.config`), is written 0600, and is hand-parsed as trivial `key = "value"` lines (no TOML dependency). `KeySource` is tracked so `auth status` can report where the key came from without printing it.

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

### Exit codes (scriptability)

`cmd/helpers.go` defines `exitErr{code, err}` and `classifyError`:
- **1**: runtime/network error
- **2**: usage error (cobra default; also `withCode(2, ...)` for local input validation)
- **3**: API error (`client.APIError`) or in-band stream failure

### Output modes

`resolveMode` (root.go): `--output pretty|json` wins; otherwise **pretty on a TTY, JSON when piped**, so `tabstack ... | jq` works without a flag. JSON streams emit NDJSON (one line per event). The renderer never reshapes caller-defined schema results.

### Input ergonomics

`readInput`/`readJSON` (helpers.go) accept a literal string, `@file`, or `-` for stdin, mirroring curl's `-d`. `readJSON` validates JSON locally so a malformed schema fails with a clear message instead of an opaque API 400.

## Conventions

- New endpoints: add a request type + method in `internal/client/`, then a leaf command in `cmd/`. Reuse `geoTarget()`, `readJSON()`, and the shared `effort`/`geo`/`nocache` flag names.
- `GeoTarget` and `Effort` (`min`/`standard`/`max`) are shared across fetch-based endpoints.
- Validate caller input locally where the server has known limits (e.g. `maxInstructionsLen = 20000` in `cmd/generate.go`).
