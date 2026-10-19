# Using `tabstack` from an AI agent

This document tells an LLM agent when to reach for the `tabstack` CLI and how to
drive it correctly. It is a reference, not a tutorial: every command, flag,
input format, output shape, and failure mode is listed so you can call the tool
without guessing.

The installed binary carries this same document: run `tabstack agent-guide` to
print the copy that matches its version.

## TL;DR for agents

- Always pass **`-o json`** so output is deterministic and parseable. Without it,
  output is pretty-printed on a TTY and only switches to JSON when piped.
- Provide an API key via the **`TABSTACK_API_KEY`** env var (preferred for
  automation) or `--api-key`. Do not rely on an interactive `auth login` prompt.
- **Branch on the exit code**, not on stderr text: `0` ok, `1` runtime/network,
  `2` bad input or missing key, `3` API error or task failure.
- `extract json` / `generate json` return **exactly the JSON shape your schema
  describes**. Nothing is wrapped or reshaped. Validate your schema before
  calling; malformed schemas fail locally with exit `2`.
- `automate` and `research` **stream**. In `-o json` they emit **NDJSON** (one
  JSON object per line); read line by line, do not `JSON.parse` the whole output.
- Rate limits are **handled for you**: a `429` is retried after the server's
  `Retry-After` (up to `--max-retries`, default 2). If it still fails, the error
  ends in `(retry after Ns)`; wait that long before calling again.

## When to use tabstack

Use it when a task needs the **live content of a web page** turned into
something a program can use, or a **browser** to act on a site.

| You need to… | Use |
|--------------|-----|
| Read a page as text: docs, an article, a README | `extract markdown <url>` |
| Pull specific fields from a page into a fixed shape (prices, listings, specs) | `extract json <url> --schema …` |
| Fields that need judgement, not just copying (summaries, classification, normalisation) | `generate json <url> --instructions … --schema …` |
| Answer a question that spans several sources, with citations | `agent research <query>` |
| Click, type, log in, paginate, or fill a form on a site | `agent automate <task> --url …` |
| A ready-made schema for a common page type (job postings, products, …) | `schema list`, then `schema pull <name>` and `--schema-name <name>` |

Prefer the cheapest tool that answers the question: `extract markdown` before
`generate json`, and both before `agent automate`. Automation drives a real
browser, so it is the slowest and most expensive option.

Do **not** use it for:

- Content you already have or can read locally (files, a cloned repo).
- A public JSON API you can call directly; fetching it through a browser adds
  cost and nothing else.
- Tasks that depend on *your user's own* browser state (their logged-in
  sessions, extensions, local files); `automate` drives a remote browser, not
  theirs.

**CLI or MCP?** If your host speaks the Model Context Protocol (Claude Desktop,
an IDE, an agent framework), `tabstack mcp` exposes the same capabilities as
tools (`extract_markdown`, `extract_json`, `generate_json`, `automate`,
`research`, `schema_list`, `schema_resolve`) with streamed progress, and is
usually simpler than shelling out. Use the CLI when you only have a shell, or
need to pipe results through other commands.

## Setup

```bash
export TABSTACK_API_KEY="<key>"        # preferred for non-interactive use
# optional:
export TABSTACK_BASE_URL="<url>"       # override API root
```

Key resolution precedence (highest first): `--api-key` flag → `TABSTACK_API_KEY`
→ config file (`~/.config/tabstack/config.toml`). If no key is found, commands
that hit the API exit `2` (non-retryable config error) with a clear message.

## Global flags (valid on every command)

| Flag | Effect |
|------|--------|
| `-o, --output pretty\|json` | **Set `json` for agents.** Default auto-detects (pretty on TTY, json when piped). |
| `--api-key <key>` | API key, overrides env + config. |
| `--base-url <url>` | API root, overrides env + config. |
| `--no-color` | Disable ANSI colour (also honours `NO_COLOR`). Irrelevant under `-o json`. |
| `--timeout <dur>` | Timeout for **non-streaming** calls only, e.g. `30s`, `2m`. Ignored by `automate`/`research`. |
| `--org <org>` | Act as another stored organisation for this one call (id, name, or unique prefix). Never changes the saved default. |
| `--max-retries <n>` | Retries for a `429` (or a `503` with `Retry-After`), default `2`; `0` disables. See [Rate limits](#rate-limits). |
| `--debug` | Print trace id, timing, and rate-limit state for each API call to **stderr** (a JSON object under `-o json`). Quote the trace id in support requests. |

## Input value convention

`--schema`, `--instructions`, and `--data` each accept one of three forms:

- a **literal string**: `--schema '{"type":"object"}'`
- **`@file`**: `--schema @schema.json` (reads the file)
- **`-`**: `--schema -` (reads stdin; only one flag per call may use `-`)

JSON-valued flags are validated locally before the request; invalid JSON fails
with exit `2` and no network call.

## Commands

### `extract markdown <url>`: page → clean Markdown

Non-streaming. Single JSON response.

| Flag | Required | Notes |
|------|----------|-------|
| `--effort min\|standard\|max` | no | Fetch effort, default `standard`. See table below. |
| `--geo <CC>` | no | ISO 3166-1 alpha-2 country, e.g. `GB`. |
| `--metadata` | no | Include page metadata (title, author, …) in the response. |
| `--nocache` | no | Bypass cache, fetch fresh. |

Output (`-o json`):
```json
{"content":"# Title…","url":"https://…","metadata":{"title":"…","author":"…"}}
```
`metadata` is present only when `--metadata` was passed.

Example:
```bash
tabstack -o json extract markdown https://example.com --metadata
```

### `extract json <url> --schema …`: page → schema-shaped JSON

Non-streaming. **The response is exactly your schema's shape**, returned verbatim.

| Flag | Required | Notes |
|------|----------|-------|
| `--schema` | one of | JSON schema (literal / `@file` / `-`). Must be valid JSON. |
| `--schema-name <name>` | one of | A schema pulled with `schema pull`, by bare name or repo path. Mutually exclusive with `--schema`. |
| `--storage <dir>` | no | Schema store to read `--schema-name` from (default: the config dir). |
| `--effort` / `--geo` / `--nocache` | no | As above. |

Example:
```bash
tabstack -o json extract json https://example.com \
  --schema '{"type":"object","properties":{"title":{"type":"string"}}}'
```

### `generate json <url> --instructions … --schema …`: fetch + AI transform → schema-shaped JSON

Non-streaming. Fetches the page, then transforms its content with AI per your
instructions into the schema shape. Response is your schema's shape, verbatim.

| Flag | Required | Notes |
|------|----------|-------|
| `--instructions` | **yes** | Transform prompt (literal / `@file` / `-`). Max **20,000** chars (validated locally). |
| `--schema` / `--schema-name` | **one of** | Output JSON schema, inline or pulled. Same rules as `extract json`. |
| `--effort` / `--geo` / `--nocache` | no | As above. |

Constraint: `--instructions` and `--schema` cannot **both** read from `-` (stdin)
in one call.

Example:
```bash
tabstack -o json generate json https://example.com \
  --instructions "Extract the headline and a one-sentence summary." \
  --schema @out-schema.json
```

### `agent automate <task>`: natural-language browser automation (streaming)

Runs server-side and **streams events**. The task description is the positional
argument.

| Flag | Required | Notes |
|------|----------|-------|
| `--url <url>` | no | Starting URL for the task. |
| `--data <json>` | no | JSON context object (literal / `@file` / `-`), e.g. form values. |
| `--guardrails <text>` | no | Safety constraints, e.g. "read-only, do not submit forms". |
| `--max-iterations <n>` | no | 1–100 (validated locally). |
| `--max-validation-attempts <n>` | no | 1–10 (validated locally). |
| `--geo <CC>` | no | Geotarget country code. |
| `--interactive` | no | Allow the task to **pause and request input** mid-run (see `agent input`). |

Output (`-o json`): NDJSON, one event per line. Event names include `task:started`,
`agent:processing`, `browser:navigated`, `agent:extracted`, `task:completed`,
`complete`, `done`, and `error`. Read incrementally.

**Failure is signalled in-band**, not by exit status of the stream itself: a
`task:completed`/`complete` event with `"success":false`, or an `error` event,
causes the command to exit `3` after the stream ends.

Example:
```bash
tabstack -o json agent automate "Find the Pro plan price" --url https://example.com
```

### `agent input <request-id> --data …`: answer a paused automation

Non-streaming. Use **only** when an `--interactive` automation emitted an event
asking for input; take the request ID from that event.

`--data` (required) must be a JSON object, one of:
- provide values: `{"fields":[{"ref":"<field-ref>","value":"<answer>"}]}`
- decline: `{"cancelled":true}`

Setting neither `fields` nor `cancelled` exits `2`.

```bash
tabstack agent input req_abc123 --data '{"fields":[{"ref":"otp","value":"123456"}]}'
```

### `agent research <query>`: web research with citations (streaming)

Streams events, then prints a synthesised answer with numbered sources.

| Flag | Required | Notes |
|------|----------|-------|
| `--mode fast\|balanced` | no | `fast` (default): quick answers. `balanced`: deeper multi-source. |
| `--fetch-timeout <sec>` | no | Per-page fetch timeout in seconds (integer). |
| `--nocache` | no | Force fresh research. |

The query is the positional argument, max **10,000** chars (validated locally).
Same in-band failure semantics as `automate` (exit `3` on failure).

Output (`-o json`): NDJSON event stream; the `complete` event carries the final
answer and (when present) a `metadata.citedPages` list (the sources, in citation
order). Research terminates on `complete` (or `error`); it has **no** `done`
event, unlike `automate`. Waiting for `done` will hang.

`metadata.citedPages` is **optional** and is omitted in `fast` mode (the
default), so a default call may return no sources. Agents that need citations
should pass `--mode balanced` and null-guard `citedPages` before reading it.

```bash
tabstack -o json agent research "latest developments in quantum computing" --mode balanced
```

### `schema list` / `schema pull`: ready-made extraction schemas

The public schema library has schemas for common page types. `schema list`
shows them (`--local` for only the pulled ones); `schema pull <name>` saves one
locally so `extract json` and `generate json` can use it via `--schema-name`.
These commands need no API key.

```bash
tabstack -o json schema list
tabstack schema pull job-posting
tabstack -o json extract json https://example.com/careers/123 --schema-name job-posting
```

### `auth login` / `auth status`

- `auth login`: opens a browser for an OAuth sign-in, then sets up an API key.
  It needs a human at a browser, so **agents should use `TABSTACK_API_KEY`**
  and skip it.
- `auth status`: reports who is signed in, the active organisation, and which
  key is in play and where it came from; never prints the key. Works without a
  key present.

## Rate limits

A `429 Too Many Requests` (or a `503` that carries `Retry-After`) is retried
automatically, after the wait the server asks for (`Retry-After`, else the
rate-limit reset header, else 1s then 2s). Each retry prints a notice to
**stderr**, never stdout:

```json
{"retry":{"status":429,"attempt":1,"max_retries":2,"wait_ms":3000}}
```

The client does not block on a wait longer than 30s. When retries run out, or
the wait is too long, the command exits `3` with an error ending in
`(retry after Ns)` when the server named a wait. Sleep that long, then retry the
same command unchanged; this is the one exit `3` that is not fixed by changing
the request. Pass `--debug` to see the remaining quota on every call.

Streams are only retried before they start. Once events are flowing, a failure
is reported in-band and the run is not replayed.

## Effort levels (`extract`, `generate`)

| Value | Behaviour | Latency |
|-------|-----------|---------|
| `min` | Fastest, no fallback | ~1–5s |
| `standard` | Balanced (default) | ~3–15s |
| `max` | Full browser rendering for JS-heavy sites | ~15–60s |

Pick `max` only when a page needs JavaScript to render; it is the slowest and
most expensive.

## Exit codes

| Code | Meaning | Agent action |
|------|---------|--------------|
| `0` | success | proceed |
| `1` | runtime / network error | retry with backoff; check connectivity, timeout. |
| `2` | usage / invalid input or missing config | **fix the command**: bad flag, missing required arg, malformed JSON, out-of-range value, or no API key configured. Do not retry unchanged. |
| `3` | API error or in-band task failure | inspect the error message / failed event; the request reached the API but was rejected or the task failed. Adjust the request (URL, task wording, schema) before retrying. **Exception:** a `429` error (message ends in `retry after Ns`) means wait, then retry unchanged. |

Errors in `-o json` mode are written to **stderr** as `{"error":"<message>"}`.

## Gotchas

- **`extract json` / `generate json` never wrap the result.** You get exactly the
  shape your schema defines. Don't expect an envelope.
- **A 4xx rejection (typically 400) means the input was unprocessable** (bad URL, schema, or
  task): exit `3`. Retrying with higher `--effort` will **not** fix it; fix the
  input. Higher effort/`--nocache` only helps transient fetch problems.
- **Streaming output is NDJSON, not a single JSON document.** Parse per line.
- **`--timeout` does not apply to `automate`/`research`** (a hard timeout would cut
  the stream). Cancel by killing the process instead.
- **`agent input` is unreachable without `--interactive`** on the original run.
- **One stdin per call.** At most one `-` flag per invocation.
