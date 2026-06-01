# tabstack

A command-line client for the [Tabstack AI API](https://tabstack.ai): browser
automation, web research, and structured extraction and generation from any URL.

## Install

```bash
go install github.com/Mozilla-Ocho/tabstack-cli/cmd/tabstack@latest
```

Or build from source:

```bash
git clone https://github.com/Mozilla-Ocho/tabstack-cli.git
cd tabstack-cli
make build        # produces ./bin/tabstack
```

## Authentication

Get an API key from your Tabstack account, then store it:

```bash
tabstack auth login            # prompts for the key, saves it to the config file
tabstack auth status           # shows how your key is being resolved (never prints it)
```

A key can come from three sources, highest precedence first:

1. `--api-key` flag
2. `TABSTACK_API_KEY` environment variable
3. config file at `$XDG_CONFIG_HOME/tabstack/config.toml` (defaults to `~/.config/tabstack/config.toml`)

The base URL can likewise be set with `--base-url` or `TABSTACK_BASE_URL`.

## Usage

### Extract

```bash
# Convert a page to clean Markdown
tabstack extract markdown https://example.com --metadata

# Extract structured data shaped by a JSON schema
tabstack extract json https://example.com --schema @schema.json
tabstack extract json https://example.com --schema '{"type":"object","properties":{"title":{"type":"string"}}}'
```

### Generate

```bash
# Fetch a page and transform it with AI into your schema
tabstack generate json https://example.com \
  --instructions "Summarise the article and list key points." \
  --schema @schema.json
```

### Agent

```bash
# Browser automation (streams progress events)
tabstack agent automate "Find the pricing for the Pro plan" --url https://example.com

# Web research (streams progress; prints a report with cited sources)
tabstack agent research "What is the capital of France?" --mode fast

# Respond to a paused automation that asked for input
tabstack agent input <request-id> --data '{"answer":"yes"}'
```

`--schema`, `--instructions`, and `--data` all accept a literal string, `@file`
to read a file, or `-` to read from stdin.

## Output

Output is **pretty** (styled, human-readable) on a terminal and **JSON** when
piped, so it composes with tools like `jq`:

```bash
tabstack extract markdown https://example.com | jq .
```

Force a mode with `-o/--output pretty|json`, or disable colour with `--no-color`
(or the `NO_COLOR` env var). Streaming commands emit one NDJSON line per event in
JSON mode.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | success |
| `1`  | runtime / network error |
| `2`  | usage / invalid input |
| `3`  | API error or in-band task failure |

These make the CLI scriptable — branch on the exit status to tell a bad request
from a network failure from an API rejection.

## Development

```bash
make build        # build into ./bin
make test         # go test ./...
make lint         # gofmt -w . && go vet ./...
make smoke        # live API smoke test (needs a key; SKIP_AGENT=1 to skip costly calls)
make help         # list all targets
```

See [CLAUDE.md](CLAUDE.md) for an architecture overview. The API surface is
described in [openapi.yaml](openapi.yaml).

## License

[MIT](LICENSE)
