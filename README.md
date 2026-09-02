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
curl -fsSL https://tabstack.ai/install.sh | sh   # installs tabstack to /usr/local/bin
```

**From source** (needs Go):

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
tabstack auth login                              # sign in in your browser, once
tabstack extract markdown https://example.com    # confirm it works
```

That's it. From here, every command follows the same `tabstack <group> <action> <target>` shape.

## Authentication

Sign in with your browser, once:

```bash
tabstack auth login           # opens the console, signs you in, sets up an API key
tabstack auth status          # who you are, which org you're acting as, which key is in play
```

`auth login` runs an OAuth 2.1 authorization code flow with PKCE: it opens
`https://console.tabstack.ai` in your browser and waits on a loopback listener
(`http://127.0.0.1:<random-port>/callback`) for the redirect back. Nothing is
pasted, and no secret is stored in your shell history. If a browser cannot be
opened, the printed URL still works: open it by hand and the login completes.
On a machine with no display at all, login fails with a pointer to
`TABSTACK_API_KEY`, which is the right credential for CI.

### The org and key model

Two different credentials, and they are never interchangeable:

| | Session | API key |
|---|---|---|
| Scope | your user | one organisation |
| Created by | `tabstack auth login` | `tabstack keys create` |
| Sent to | `console.tabstack.ai` (sign-in and management) | `api.tabstack.ai` (every product command) |
| Lifetime | expires, refreshed automatically | until revoked |

Because keys are org scoped, the CLI stores **one key per organisation** and
tracks which org is active. Product commands send the active org's key.

```bash
tabstack auth switch                 # pick from a list (shows role and whether a key is stored)
tabstack auth switch acme            # id, exact name, or a unique name prefix
tabstack extract markdown "$url" --org other-co   # act as another org for one command only
```

Switching is a credential change, not a display setting: it decides which key
goes out. It never signs you in again: the session is user scoped, so switching
orgs makes no OAuth request at all. An argument is resolved by exact id, then
exact name (case-insensitive), then unique name prefix; an ambiguous prefix lists
every match with its id and exits non-zero rather than guessing.

`--org` is a one-shot override. It never rewrites your active org, it prints the
org it is acting as to stderr, and if that org has no stored key it fails with
the exact command to fix it, and it will never fall back to a different org's
credential.

### Managing keys

```bash
tabstack keys create --org acme --name cli-laptop   # prints the key once, stores it
tabstack keys list --org acme                       # previews only, never plaintext
tabstack keys use <key-id>                          # adopt an existing key into this CLI
tabstack keys use                                   # pick from a list (or the only one)
tabstack keys revoke <key-id>                       # also clears it from your config
tabstack keys revoke <key-id> --yes                 # skip the confirmation prompt
```

Keys created by the CLI are named `cli-<hostname>` by default, so they are
legible when you come to revoke one in the console.

Revoking is irreversible and breaks anything still sending that key, so
`keys revoke`, `auth logout --all`, and `auth sessions --revoke` ask before
acting. Pass `--yes` to skip the prompt; on a non-interactive terminal they
refuse with exit 2 rather than proceeding unasked. (`--force`, on
`schema pull`, is a different thing: it overwrites a local file.)

`keys use` reveals one of the organisation's existing keys and stores it as the
one this CLI sends, replacing whatever was stored. Pass a key id (from
`keys list`) to pick directly; with no id, a single key is adopted automatically
and multiple keys prompt you to choose.

### Where a product key comes from

Resolved in this order, highest precedence first:

1. `--api-key` (also accepted as `--key`)
2. `TABSTACK_API_KEY` environment variable
3. the stored key for a `--org` override, when given
4. the stored key for the active org
5. a key from a pre-organisation config, only while no org is active

If `TABSTACK_API_KEY` is set it wins, and `auth status` says so explicitly rather
than showing a stored org key that is not actually being used. If nothing
resolves, commands exit `2` with guidance.

### Sessions and signing out

```bash
tabstack auth sessions              # list your CLI sessions, current one marked
tabstack auth sessions --revoke <id>
tabstack auth logout               # revoke this session; --all revokes every session
```

Logging out clears the session and refresh token. It leaves your API keys in
place; removing those is `tabstack keys revoke`.

### Config and endpoints

Credentials live at `$XDG_CONFIG_HOME/tabstack/config.toml` (defaults to
`~/.config/tabstack/config.toml`), written `0600` inside a `0700` directory and
saved atomically. If the file is group- or world-readable the CLI warns and
tells you how to fix it, but still runs.

```bash
tabstack config show     # every org and its key state, session, endpoints, permissions
tabstack config path     # for scripting: cat "$(tabstack config path)"
```

`config show` redacts every key and token to its first and last four characters,
so the output is safe to paste into a bug report. There is no flag that prints
them in full. Unlike `auth status`, which covers only the org you are acting as,
`config show` lists every org the CLI knows about: it answers "which credential
would go out if I switched".

| Endpoint | Config key | Env var | Flag |
|----------|-----------|---------|------|
| Product API | `base_url` | `TABSTACK_BASE_URL` | `--base-url` |
| Auth and management | `auth_url` | `TABSTACK_AUTH_URL` | `--auth-url` |

### Upgrading from a pre-organisation config

If you already have a config file holding a single `api_key`, it keeps working.
On first run the key is read as a legacy credential and used for product calls
while no organisation is active; it is not deleted, and nothing is rewritten
until the next command that saves config. After `tabstack auth login` gives you
an active org with its own key, the legacy key is ignored but left in the file.
Clear it out when you're ready:

```bash
tabstack config show               # confirm the active org has a key of its own
tabstack config drop-legacy-key    # then remove the old one
```

`drop-legacy-key` refuses while the legacy key is still the credential in use, so
it cannot leave you with nothing that works. `--force` overrides that.

## Commands

| Command | What it does |
|---------|--------------|
| `tabstack extract markdown <url>` | Convert a page to clean Markdown |
| `tabstack extract json <url> --schema …` | Extract structured data shaped by a JSON schema |
| `tabstack generate json <url> --instructions … --schema …` | Fetch a page and transform it with AI into your schema |
| `tabstack agent automate <task> [--url …]` | Run a natural-language browser-automation task (streams) |
| `tabstack agent research <query>` | Research the web and print a cited report (streams) |
| `tabstack agent input <request-id> --data …` | Answer a paused `--interactive` automation |
| `tabstack schema list` | List the pre-defined extraction schemas in the library |
| `tabstack schema pull <selector…>` | Pull schemas into a local store for use with `extract`/`generate` |
| `tabstack schema status` | Show which pulled schemas are locally modified or out of date |
| `tabstack schema path <name>` | Print the local file path of a pulled schema |
| `tabstack schema rm <selector…>` | Remove pulled schemas from the local store |
| `tabstack auth login` | Sign in with your browser (OAuth 2.1 + PKCE) |
| `tabstack auth status` | Show your identity, active org, and which key is in play |
| `tabstack auth switch [org]` | Change the organisation your commands act as |
| `tabstack auth sessions` | List (and revoke) your CLI sessions |
| `tabstack auth logout` | Revoke this session, or all of them with `--all` |
| `tabstack keys create` / `list` / `use` / `revoke` | Manage an organisation's API keys |
| `tabstack config show` | Show stored config for every org, secrets redacted |
| `tabstack config path` | Print the config file path |
| `tabstack config drop-legacy-key` | Remove a pre-organisation API key once an org key is in place |
| `tabstack mcp` | Run a local MCP server exposing Tabstack tools over stdio |

Run `tabstack <command> --help` for the full flag list on any command.

### Extract

```bash
# Convert a page to clean Markdown (add --metadata for title/author/etc.)
tabstack extract markdown https://example.com --metadata

# Extract structured data shaped by a JSON schema
tabstack extract json https://example.com --schema @schema.json
tabstack extract json https://example.com --schema '{"type":"object","properties":{"title":{"type":"string"}}}'

# …or reference a schema you pulled with `tabstack schema pull` by name
tabstack extract json https://example.com --schema-name job-posting
```

### Generate

```bash
# Fetch a page and transform it with AI into your schema
tabstack generate json https://example.com \
  --instructions "Summarise the article and list the key points." \
  --schema @schema.json

# …or use a pulled schema by name (same --schema-name sugar as extract)
tabstack generate json https://example.com \
  --instructions "Summarise the article and list the key points." \
  --schema-name news-article
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

### Schema

Pull ready-made extraction schemas from the
[tabstack-schemas](https://github.com/Mozilla-Ocho/tabstack-schemas) library,
then feed them to `extract json`. Pulled schemas land in
`$XDG_CONFIG_HOME/tabstack/schemas` (or `~/.config/tabstack/schemas`) by default,
mirroring the repo's `category/name.json` layout.

```bash
# Browse what's available (pulled schemas are marked with a ✓)
tabstack schema list
tabstack schema list --local                   # only what you've pulled (offline)

# Pull by name, by category, or by full path
tabstack schema pull job-posting
tabstack schema pull jobs                      # every schema in the "jobs" category
tabstack schema pull jobs/job-posting.json
tabstack schema pull --all                     # the whole library

# Use a pulled schema by name (no need to spell out the path)
tabstack schema pull product-listing
tabstack extract json https://example.com --schema-name product-listing

# …or keep a separate store and point both commands at it
tabstack schema pull product-listing --storage ./schemas
tabstack extract json https://example.com --schema-name product-listing --storage ./schemas
```

`--schema-name` resolves locally against the store (it never hits the network),
so it works offline once a schema is pulled. A name that matches more than one
stored schema is rejected; pass the full `category/name.json` path to
disambiguate.

Pull records what it fetched, so you can see how your local copies relate to the
library and tidy up:

```bash
# What have I changed, and what's drifted upstream?
tabstack schema status            # "modified" = your edits, "outdated" = upstream changed
tabstack schema status --local    # skip the network; only flag local edits

# Print a path (handy for scripting or other tools)
tabstack extract json https://example.com --schema @"$(tabstack schema path job-posting)"

# Remove pulled schemas you no longer need
tabstack schema rm job-posting
```

Re-running `schema pull` on an `outdated` schema fetches the latest version
(prompting before it overwrites local edits). The library index is cached per
store for an hour; pass `--refresh` to `list`/`pull` to refetch immediately.
Shell completion suggests schema names for `pull`, `rm`, `path`, and
`--schema-name`.

A selector is a schema name, a category, or a full repo path. When a pulled
schema already exists locally and differs from the library, you're prompted to
**overwrite**, **keep** your local copy, or **quit**, so customising a schema
and re-pulling later never silently discards your edits. Use `--force` to
overwrite without prompting. In a non-interactive shell a conflict fails (exit 2)
unless `--force` is given.

### MCP server

`tabstack mcp` runs a local [Model Context Protocol](https://modelcontextprotocol.io)
server over stdio, so an MCP client (Claude Desktop, an IDE, another agent) can
call Tabstack as tools: `extract_markdown`, `extract_json`, `generate_json`,
`automate`, `research`, `schema_list` / `schema_resolve`, and read-only account
context (`whoami`, `list_orgs`, `active_org`).

Sign in first (`tabstack auth login`) or set `TABSTACK_API_KEY`. Product calls
use your org-scoped API key; if you are signed in but have no key stored, one is
created for the active organisation on first start.

#### Add to Claude Desktop

1. Open **Settings → Developer → Edit Config** (this opens `claude_desktop_config.json`), or edit it directly:
   - macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
   - Windows: `%APPDATA%\Claude\claude_desktop_config.json`
2. Add a `tabstack` entry under `mcpServers`:

   ```json
   {
     "mcpServers": {
       "tabstack": {
         "command": "tabstack",
         "args": ["mcp"]
       }
     }
   }
   ```

3. Restart Claude Desktop. The Tabstack tools appear in the tools menu (🔌).

If Claude Desktop reports it can't find the command, use the absolute path to
the binary instead, since its `PATH` differs from your shell's. Find it with
`which tabstack` and set `"command"` to that (the install script puts it at
`/usr/local/bin/tabstack`).
To pin an API key for a non-interactive setup, add
`"env": { "TABSTACK_API_KEY": "…" }` to the entry.

stdout carries the JSON-RPC protocol; logs go to stderr. Closing the connection
(or Ctrl-C) shuts the server down cleanly.

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

**`--no-cache`**: bypass the cache and fetch fresh. The older spelling
`--nocache` still works as a hidden alias, so existing scripts keep running.

**Global flags** (valid on every command):

| Flag | Description |
|------|-------------|
| `--api-key <key>` | API key (overrides env and stored keys) |
| `--org <org>` | Act as this organisation for one command (id, name, or unique prefix) |
| `--base-url <url>` | Product API base URL |
| `--auth-url <url>` | Auth and management host URL |
| `-o, --output pretty\|json` | Force an output mode (default: auto-detect) |
| `--no-color` | Disable coloured output (or set `NO_COLOR`) |
| `--timeout <dur>` | Request timeout for non-streaming calls (default `2m`); `0` disables |
| `--debug` | Print request id, timing, and rate-limit headers to stderr per API call |

**`--debug`**: for each API call, print a line to **stderr** (so it never
touches piped stdout) with the HTTP status, elapsed time to first byte, the
`x-trace-id` request id to quote in a support request, and the rate-limit
budget. In `--output json` mode the line is a JSON object instead.

```console
$ tabstack extract markdown https://example.com --debug
debug POST 200 359ms  trace=e1923436412e811be9fe229131cb0694  ratelimit=999/1000 (resets in 20s)
```

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
