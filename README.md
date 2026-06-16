# tabstack

> Every web interaction your agent or stack needs: browser automation, web research, and structured extraction from any URL.

[![CI](https://github.com/Mozilla-Ocho/tabstack-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/Mozilla-Ocho/tabstack-cli/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Mozilla-Ocho/tabstack-cli.svg)](https://pkg.go.dev/github.com/Mozilla-Ocho/tabstack-cli)
[![Go Report Card](https://goreportcard.com/badge/github.com/Mozilla-Ocho/tabstack-cli)](https://goreportcard.com/report/github.com/Mozilla-Ocho/tabstack-cli)
[![Release](https://img.shields.io/github/v/release/Mozilla-Ocho/tabstack-cli?include_prereleases&sort=semver)](https://github.com/Mozilla-Ocho/tabstack-cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`tabstack` is a single-binary CLI client for the [Tabstack AI API](https://tabstack.ai):
every web interaction your agent or stack needs, from the terminal or a script.
It turns any URL into clean Markdown or schema-shaped JSON, runs natural-language
browser automation, and answers research questions with cited sources, all with
output that's pretty in a terminal and pipeable into `jq`.

```console
$ tabstack extract markdown https://example.com --metadata
Example Domain
example.com

# Example Domain

This domain is for use in illustrative examples in documents...
```

## Contents

- [Features](#features)
- [Install](#install)
- [Quick start](#quick-start)
- [Authentication](#authentication)
- [Commands](#commands)
- [Common options](#common-options)
- [Output & scripting](#output--scripting)
- [Exit codes](#exit-codes)
- [Using tabstack with AI agents](#using-tabstack-with-ai-agents)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Extract**: convert any page to clean Markdown, or pull structured data shaped by your own JSON schema.
- **Generate**: fetch a page and transform it with AI into the JSON shape you describe.
- **Automate**: run natural-language browser tasks server-side, streaming progress as they go.
- **Research**: search the web, synthesise an answer, and print it with numbered, cited sources.
- **Scriptable**: pretty output on a TTY, JSON when piped; meaningful exit codes for branching in scripts.
- **No dependencies to run**: a single static Go binary; pre-built for macOS, Linux, and Windows.

## Install

**macOS / Linux, quickest:**

```bash
git clone https://github.com/Mozilla-Ocho/tabstack-cli.git
cd tabstack-cli
make install-local   # builds and copies to /usr/local/bin, works in any terminal immediately
```

**Pre-built binaries** (no Go required) are on the
[Releases page](https://github.com/Mozilla-Ocho/tabstack-cli/releases).

**Go developers** (`go install` puts the binary in `$GOPATH/bin`, usually `~/go/bin`):

```bash
go install github.com/Mozilla-Ocho/tabstack-cli/cmd/tabstack@latest
```

If `tabstack` is not found afterwards, add `~/go/bin` to your `PATH`:

```bash
# Add to ~/.zshrc or ~/.bashrc, then restart your terminal
export PATH="$HOME/go/bin:$PATH"
```

## Quick start

```bash
tabstack auth login                              # paste your API key once
tabstack extract markdown https://example.com    # confirm it works
```

That's it. From here, every command follows the same `tabstack <group> <action> <target>` shape.

## Authentication

Get an API key from your Tabstack account, then store it:

```bash
tabstack auth login            # prompts for the key (input hidden), saves it to the config file
tabstack auth status           # shows how your key is being resolved (never prints it)
```

A key can come from three sources, highest precedence first:

1. `--api-key` flag
2. `TABSTACK_API_KEY` environment variable
3. config file at `$XDG_CONFIG_HOME/tabstack/config.toml` (defaults to `~/.config/tabstack/config.toml`, written `0600`)

If no key is found, API commands exit `2` with guidance on setting one.

The base URL can likewise be set with `--base-url` or `TABSTACK_BASE_URL`.

## Commands

| Command | What it does |
|---------|--------------|
| `tabstack extract markdown <url>` | Convert a page to clean Markdown |
| `tabstack extract json <url> --schema …` | Extract structured data shaped by a JSON schema |
| `tabstack generate json <url> --instructions … --schema …` | Fetch a page and transform it with AI into your schema |
| `tabstack agent automate <task> [--url …]` | Run a natural-language browser-automation task (streams) |
| `tabstack agent research <query>` | Research the web and print a cited report (streams) |
| `tabstack agent input <request-id> --data …` | Answer a paused `--interactive` automation |
| `tabstack auth login` / `status` | Manage your API credentials |

Run `tabstack <command> --help` for the full flag list on any command.

### Extract

```bash
# Convert a page to clean Markdown (add --metadata for title/author/etc.)
tabstack extract markdown https://example.com --metadata

# Extract structured data shaped by a JSON schema
tabstack extract json https://example.com --schema @schema.json
tabstack extract json https://example.com --schema '{"type":"object","properties":{"title":{"type":"string"}}}'
```

### Generate

```bash
# Fetch a page and transform it with AI into your schema
tabstack generate json https://example.com \
  --instructions "Summarise the article and list the key points." \
  --schema @schema.json
```

### Agent

```bash
# Browser automation (streams progress events)
tabstack agent automate "Find the pricing for the Pro plan" --url https://example.com

# Web research (streams progress; prints a report with cited sources)
tabstack agent research "What are the latest developments in quantum computing?" --mode balanced

# Let an automation pause to ask you for input mid-run
tabstack agent automate "Log in and download the latest invoice" --url https://example.com --interactive

# Respond to a paused automation that asked for input (provide field values)
tabstack agent input <request-id> --data '{"fields":[{"ref":"field1","value":"yes"}]}'
# …or decline the request
tabstack agent input <request-id> --data '{"cancelled":true}'
```

`agent input` only applies to runs started with `--interactive`. Without that
flag an automation never pauses for input.

## Common options

**Input values**: `--schema`, `--instructions`, and `--data` each accept a
literal string, `@file` to read from a file, or `-` to read from stdin (the same
ergonomics as `curl -d`):

```bash
echo '{"type":"object"}' | tabstack extract json https://example.com --schema -
```

**`--effort`** (`extract`, `generate`): the speed/capability tradeoff when fetching:

| Value | Behaviour |
|-------|-----------|
| `min` | Fastest, no fallback (~1–5s) |
| `standard` | Balanced, default (~3–15s) |
| `max` | Full browser rendering for JS-heavy sites (~15–60s) |

**`--geo <CC>`**: route the fetch through a given country (ISO 3166-1 alpha-2, e.g. `GB`, `US`, `JP`).

**`--nocache`**: bypass the cache and fetch fresh.

**Global flags** (valid on every command):

| Flag | Description |
|------|-------------|
| `--api-key <key>` | API key (overrides env and config file) |
| `--base-url <url>` | API base URL |
| `-o, --output pretty\|json` | Force an output mode (default: auto-detect) |
| `--no-color` | Disable coloured output (or set `NO_COLOR`) |
| `--timeout <dur>` | Request timeout for non-streaming calls, e.g. `30s` |

## Output & scripting

Output is **pretty** (styled, human-readable) on a terminal and **JSON** when
piped, so it composes with tools like `jq` without a flag:

```bash
tabstack extract markdown https://example.com | jq .
```

Force a mode with `-o/--output pretty|json`, or disable colour with `--no-color`
(or the `NO_COLOR` env var). Streaming commands (`automate`, `research`) emit one
NDJSON line per event in JSON mode.

> **Note:** streaming events are parsed with a 4&nbsp;MB per-event buffer. A
> single event whose payload exceeds that (e.g. an extremely large extracted
> page in one frame) ends the stream with a parse error. This is well above
> normal event sizes; if you hit it, build from source with a larger SSE buffer.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | success |
| `1`  | runtime / network error |
| `2`  | usage / invalid input or missing config (e.g. no API key) |
| `3`  | API error or in-band task failure |

These make the CLI scriptable: branch on the exit status to tell a bad request
from a network failure from an API rejection:

```bash
if ! tabstack extract markdown "$url" > out.md; then
  case $? in
    2) echo "check your arguments" ;;
    3) echo "the API rejected the request" ;;
    *) echo "network or runtime error" ;;
  esac
fi
```

## Using tabstack with AI agents

`tabstack` is designed to be driven by LLM agents as well as humans. If you're
wiring it into an agent (Claude Code, a custom harness, etc.), point the agent at
[AGENTS.md](AGENTS.md). It documents every command, flag, and exit code in a
form tuned for machine consumption.

## Development

```bash
make build        # build into ./bin/tabstack
make test         # go test ./...
make lint         # gofmt -w . && go vet ./...
make smoke        # live API smoke test (needs a key; SKIP_AGENT=1 to skip costly calls)
make help         # list all targets
```

See [CLAUDE.md](CLAUDE.md) for an architecture overview; the API surface is
described in [openapi.yaml](openapi.yaml).

## Contributing

Contributions are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). This project
follows the [Mozilla Community Participation Guidelines](CODE_OF_CONDUCT.md). To
report a security issue, see [SECURITY.md](SECURITY.md).

## Releases

Tagged releases (`vMAJOR.MINOR.PATCH`) build cross-platform binaries via
[goreleaser](https://goreleaser.com). Build a local snapshot with `make snapshot`.

## License

[MIT](LICENSE) © Mozilla
