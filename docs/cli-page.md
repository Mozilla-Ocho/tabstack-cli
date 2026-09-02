---
title: CLI
description: Install and use the tabstack command-line client.
---

# Tabstack CLI

`tabstack` is a single-binary command-line client for the Tabstack API. It turns
any URL into clean Markdown or schema-shaped JSON, runs natural-language browser
automation, and answers research questions with cited sources. Output is styled
in a terminal and pipeable into `jq` everywhere else.

```console
$ tabstack extract markdown https://example.com --metadata
Example Domain
example.com

# Example Domain

This domain is for use in illustrative examples in documents...
```

## Install

**macOS and Linux:**

```bash
curl -fsSL https://tabstack.ai/install.sh | sh
```

**Go:**

```bash
go install github.com/Mozilla-Ocho/tabstack-cli/cmd/tabstack@latest
```

Pre-built binaries for macOS, Linux, and Windows are on the
[releases page](https://github.com/Mozilla-Ocho/tabstack-cli/releases).

## Sign in

```bash
tabstack auth login
tabstack extract markdown https://example.com
```

`auth login` runs an **OAuth 2.1 authorization code flow with PKCE**. It opens
the console in your browser and waits on a loopback listener at
`http://127.0.0.1:<random-port>/callback`. Nothing is pasted and no secret
reaches your shell history. If a browser cannot be opened, the printed URL still
works. On a machine with no display, login fails and points you at
`TABSTACK_API_KEY`, which is the right credential for CI.

## Two credentials

They are never interchangeable.

| | Session | API key |
|---|---|---|
| Scope | your user | one organisation |
| Created by | `tabstack auth login` | `tabstack keys create` |
| Sent to | `console.tabstack.ai` | `api.tabstack.ai` |
| Lifetime | expires, refreshed automatically | until revoked |

Because keys are organisation-scoped, the CLI stores **one key per
organisation** and tracks which is active.

```bash
tabstack auth switch                # pick from a list
tabstack auth switch acme           # id, exact name, or unique prefix
tabstack auth status                # who you are, and which key is in play
```

`--org` acts as another organisation for a single command without changing the
active one:

```bash
tabstack extract markdown "$url" --org other-co
```

### Where a key comes from

Resolved in this order, highest first:

1. `--api-key` (also accepted as `--key`)
2. `TABSTACK_API_KEY`
3. the stored key for a `--org` override
4. the stored key for the active organisation
5. a pre-organisation key from an older config, only while no org is active

**In CI, set `TABSTACK_API_KEY`.** A key passed as `--api-key` is visible in
shell history and to anyone who can run `ps` while the command is in flight.

### Managing keys

```bash
tabstack keys create --org acme --name cli-laptop   # prints the key once
tabstack keys list --org acme                       # previews only
tabstack keys use <key-id>                          # adopt an existing key
tabstack keys revoke <key-id> --yes                 # irreversible; asks first
```

## Commands

| Command | What it does |
|---------|--------------|
| `tabstack extract markdown <url>...` | Convert one or more pages to clean Markdown |
| `tabstack extract json <url>... --schema …` | Extract structured data shaped by a JSON schema |
| `tabstack generate json <url> --instructions … --schema …` | Fetch a page and transform it with AI |
| `tabstack agent automate <task> [--url …]` | Natural-language browser automation (streams) |
| `tabstack agent research <query>` | Web research with cited sources (streams) |
| `tabstack agent input <request-id> --data …` | Answer a paused `--interactive` automation |
| `tabstack schema list` | List the pre-defined extraction schemas |
| `tabstack schema pull <selector>...` | Pull schemas into a local store |
| `tabstack schema status` | Show which pulled schemas are modified or outdated |
| `tabstack schema path <name>` | Print a pulled schema's local path |
| `tabstack schema rm <selector>...` | Remove pulled schemas |
| `tabstack auth login` | Sign in with your browser |
| `tabstack auth status` | Identity, active org, credential in play |
| `tabstack auth switch [org]` | Change the acting organisation |
| `tabstack auth sessions` | List, and revoke, your CLI sessions |
| `tabstack auth logout` | Revoke this session, or all with `--all` |
| `tabstack keys create` / `list` / `use` / `revoke` | Manage an organisation's API keys |
| `tabstack config show` | Stored config for every org, secrets redacted |
| `tabstack config path` | Print the config file path |
| `tabstack config drop-legacy-key` | Remove a pre-organisation key |
| `tabstack mcp` | Run a local MCP server over stdio |

Run `tabstack <command> --help` for the full flag list on any command.

### Extract

```bash
# A page as clean Markdown
tabstack extract markdown https://example.com --metadata

# The Markdown itself into a file (a plain redirect would write JSON)
tabstack extract markdown https://example.com --raw > page.md

# Structured data shaped by a JSON schema
tabstack extract json https://example.com --schema @schema.json

# Several pages at once, or a list on stdin
tabstack extract markdown https://a.com https://b.com
tabstack extract json - --schema-name job-posting < urls.txt
```

A schema describes the **shape** you want, not example values:
`{"type":"object","properties":{"title":{"type":"string"}}}`, not
`{"title":"string"}`. The CLI hints on stderr if a schema looks like the latter,
and sends the request anyway.

### Generate

```bash
tabstack generate json https://example.com \
  --instructions "Summarise this page in three bullet points." \
  --schema @out-schema.json
```

### Agent

Both stream progress events and report failure in-band, exiting `3`.

```bash
tabstack agent automate "Find the Pro plan price" --url https://example.com
tabstack agent research "What changed in HTTP/3 in 2024?" --mode balanced

# Bound a long run; --timeout does not apply to streams
tabstack agent research "$q" --max-duration 2m
```

### Schemas

A library of pre-defined extraction schemas, pulled into a local store and fed
to `extract json` by name.

```bash
tabstack schema list
tabstack schema pull job-posting
tabstack extract json https://example.com/jobs/1 --schema-name job-posting
tabstack schema status
```

A selector is a name (`job-posting`), a category (`jobs`), or a repo path
(`jobs/job-posting.json`). The store defaults to
`$XDG_CONFIG_HOME/tabstack/schemas`; `--storage` points at another.

### MCP server

```bash
tabstack mcp
```

Exposes the API as tools an MCP client can call. Claude Desktop or IDE config:

```json
{
  "mcpServers": {
    "tabstack": { "command": "tabstack", "args": ["mcp"] }
  }
}
```

## Common options

| Flag | Meaning |
|------|---------|
| `--api-key <key>` | Override the credential (prefer `TABSTACK_API_KEY` in CI) |
| `--org <selector>` | Act as another organisation for one command |
| `-o, --output pretty\|json` | Force an output mode |
| `--no-color` | Disable colour (or set `NO_COLOR`) |
| `--timeout <dur>` | Non-streaming request timeout, default `2m`, `0` disables |
| `--retries <n>` | Retry transient failures, default `2`, `0` disables |
| `--max-duration <dur>` | Bound a whole stream (`agent automate`, `agent research`) |
| `--debug` | Request id, timing, and rate limits to stderr |

A flag is only accepted on the commands that read it, so passing `--api-key` to
`schema list` is an error rather than a silent no-op.

## Environment

| Variable | Effect |
|----------|--------|
| `TABSTACK_API_KEY` | API key for product calls; overrides every stored key |
| `TABSTACK_BASE_URL` | Product API host |
| `TABSTACK_AUTH_URL` | Console host |
| `TABSTACK_OAUTH_SCOPES` | Override the scopes requested at login |
| `NO_COLOR` | Disable coloured output |
| `XDG_CONFIG_HOME` | Where `config.toml` and pulled schemas live |

## Output and scripting

Output is **pretty** on a terminal and **JSON** when piped, so it composes with
`jq` without a flag. Streaming commands emit NDJSON, one event per line.

**stdout carries results; progress, prompts, and warnings go to stderr**, so a
pipe only ever receives data.

```bash
tabstack extract markdown https://example.com | jq -r .content
tabstack auth status -o json | jq -r .active_org
```

Note that piping `extract markdown` gives you the JSON envelope, not the
Markdown. Use `--raw`, or read `.content`.

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | success |
| `1` | runtime or network error |
| `2` | usage error, invalid input, or missing configuration |
| `3` | the API rejected the request, or a streamed task failed |

```bash
tabstack extract markdown "$url" --raw > out.md
rc=$?
if [ "$rc" -ne 0 ]; then
  case "$rc" in
    2) echo "check your arguments" ;;
    3) echo "the API rejected the request" ;;
    *) echo "network or runtime error" ;;
  esac
fi
```

### Retries and cancellation

Transient failures (408, 409, 429, 5xx) are retried twice by default with
exponential backoff and jitter, honouring `Retry-After`. A `400` or `404` is
never retried. Tune with `--retries`.

Ctrl-C cancels the request in flight rather than killing the process, prints
`cancelled` to stderr, and exits `1`.

## Shell completion

```bash
tabstack completion bash > ~/.local/share/bash-completion/completions/tabstack
tabstack completion zsh  > "${fpath[1]}/_tabstack"
tabstack completion fish > ~/.config/fish/completions/tabstack.fish
```

Completion covers subcommands, enum flag values, your organisations, and both
the schema library and your local store.

## Man page

```bash
sudo mkdir -p /usr/local/share/man/man1
tabstack man | sudo tee /usr/local/share/man/man1/tabstack.1 > /dev/null
man tabstack
```

## Uninstalling

Revoke your credentials first: deleting the local files removes your copy of a
key, not the key itself, which stays valid until revoked.

```bash
tabstack keys list                 # find the id this CLI stores
tabstack keys revoke <key-id>
tabstack auth logout               # or --all for every session
```

Then the binary and the configuration:

```bash
sudo rm /usr/local/bin/tabstack               # or "$(go env GOPATH)/bin/tabstack"
rm -rf "${XDG_CONFIG_HOME:-$HOME/.config}/tabstack"
```

That directory holds `config.toml` and the schema store; nothing is written
anywhere else. Remove any completion script or man page you installed by hand,
and leave a repository's `.tabstack.toml` alone.

## Driving the CLI from an AI agent

See [AGENTS.md](https://github.com/Mozilla-Ocho/tabstack-cli/blob/main/AGENTS.md)
in the repository, which documents every command, flag, output shape, and
failure mode as a machine-facing reference.
