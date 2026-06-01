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

- **`cmd/tabstack/main.go`** — entry point; builds the root command, runs it, and maps errors to process exit codes. It checks for any error implementing `Code() int` (the `coded` interface) and exits with that code, else 1. (Lives in its own dir so `go install .../cmd/tabstack` produces a `tabstack` binary.)
- **`cmd/`** — Cobra command tree, one file per endpoint group (`agent`, `extract`, `generate`, `auth`). Each leaf command only builds its request and calls the client.
- **`internal/client/`** — the HTTP client and per-endpoint request/response types.
- **`internal/config/`** — credential and base-URL resolution.
- **`internal/ui/`** — output rendering (pretty vs JSON), styles, spinner.

### Shared app context

`cmd/root.go` defines `app{cfg, client, renderer}` and stores it in the package-global `rootApp`. The root command's `PersistentPreRunE` populates it **once** before any subcommand runs, so leaf commands never re-resolve config or rebuild the client. Commands tagged with the `skipClient` annotation (`auth login`, `auth status`) get a renderer-only setup so they work before an API key exists.

### Config resolution priority

`config.Resolve` merges three sources, lowest to highest precedence: **config file** → **environment** (`TABSTACK_API_KEY`, `TABSTACK_BASE_URL`) → **flags** (`--api-key`, `--base-url`). The config file lives at `$XDG_CONFIG_HOME/tabstack/config.toml` (falls back to `~/.config`), is written 0600, and is hand-parsed as trivial `key = "value"` lines (no TOML dependency). `KeySource` is tracked so `auth status` can report where the key came from without printing it.

### Two request styles

The client splits on transport, not endpoint:

- **`doJSON`** — single JSON request/response. Used by `extract/json`, `extract/markdown`, `generate/json`, `automate/{id}/input`. Schema-driven endpoints (`extract/json`, `generate/json`) return `json.RawMessage` verbatim because the response shape is caller-defined by the supplied JSON schema.
- **`doStream`** — Server-Sent Events. Used by `automate` and `research`. Deliberately imposes **no** client timeout (a hard timeout would cut the stream); cancellation flows through `context`. `--timeout` only affects non-streaming calls.

`internal/client/sse.go` (`ParseSSE`) is a from-scratch SSE parser with a 4MB scanner buffer (extracted page content exceeds the default 64KB token limit).

### Streaming outcome handling

Streaming endpoints signal failure **in-band** (a `task:completed`/`complete` event with `success:false`, or an `error` event) rather than via HTTP status. `cmd/agent.go`'s `runStream` watches events to capture the final answer and failure state, then the command maps that onto an exit code. The spinner only animates in pretty mode on a real TTY (`os.Stderr`).

### Exit codes (scriptability)

`cmd/helpers.go` defines `exitErr{code, err}` and `classifyError`:
- **1** — runtime/network error
- **2** — usage error (cobra default; also `withCode(2, ...)` for local input validation)
- **3** — API error (`client.APIError`) or in-band stream failure

### Output modes

`resolveMode` (root.go): `--output pretty|json` wins; otherwise **pretty on a TTY, JSON when piped**, so `tabstack ... | jq` works without a flag. JSON streams emit NDJSON (one line per event). The renderer never reshapes caller-defined schema results.

### Input ergonomics

`readInput`/`readJSON` (helpers.go) accept a literal string, `@file`, or `-` for stdin — mirroring curl's `-d`. `readJSON` validates JSON locally so a malformed schema fails with a clear message instead of an opaque API 400.

## Conventions

- New endpoints: add a request type + method in `internal/client/`, then a leaf command in `cmd/`. Reuse `geoTarget()`, `readJSON()`, and the shared `effort`/`geo`/`nocache` flag names.
- `GeoTarget` and `Effort` (`min`/`standard`/`max`) are shared across fetch-based endpoints.
- Validate caller input locally where the server has known limits (e.g. `maxInstructionsLen = 20000` in `cmd/generate.go`).
