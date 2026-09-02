# Using `tabstack` from an AI agent

This document tells an LLM agent how to drive the `tabstack` CLI correctly. It is
a reference, not a tutorial: every command, flag, input format, output shape,
and failure mode is listed so you can call the tool without guessing.

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

## Setup

```bash
export TABSTACK_API_KEY="<key>"        # preferred for non-interactive use
# optional:
export TABSTACK_BASE_URL="<url>"       # override API root
```

Key resolution precedence (highest first): `--api-key` flag → `TABSTACK_API_KEY`
→ config file (`~/.config/tabstack/config.toml`). If no key is found, commands
that hit the API exit `2` (non-retryable config error) with a clear message.

## Global flags

Only `--output` and `--no-color` are accepted everywhere. The rest are
registered on the commands that read them, so passing one elsewhere is an
`unknown flag` error, not a silent no-op.

| Flag | Valid on | Effect |
|------|----------|--------|
| `-o, --output pretty\|json` | everything | **Set `json` for agents.** Default auto-detects (pretty on TTY, json when piped). |
| `--no-color` | everything | Disable ANSI colour (also honours `NO_COLOR`). Irrelevant under `-o json`. |
| `--api-key <key>` | `extract`, `generate`, `agent`, `mcp` | API key, overrides env + config. |
| `--base-url <url>` | `extract`, `generate`, `agent`, `mcp`, `config` | API root, overrides env + config. |
| `--auth-url <url>` | `auth`, `keys`, `config`, `mcp` | Console root, overrides env + config. |
| `--org <selector>` | `extract`, `generate`, `agent`, `keys`, `auth login` | Act as another org for one command. |
| `--timeout <dur>` | `extract`, `generate`, `agent`, `mcp` | Timeout for **non-streaming** calls only, e.g. `30s`. Defaults to `2m`; `0` disables. Never applies to `automate`/`research`. |
| `--retries <n>` | `extract`, `generate`, `agent`, `mcp` | Retry 408/409/429/5xx this many times. Defaults to `2`; `0` disables. Bounded by `--timeout`. |
| `--debug` | everything | Request id, timing, rate limits, to stderr. Only product-host calls are instrumented. |

**Every command emits JSON under `-o json`**, including `auth`, `keys`,
`config`, and `schema`, so account and configuration state is scriptable the
same way results are. Exact shapes: [`docs/`](docs/README.md).

**stdout is results; stderr is progress, prompts, and warnings.** Read stdout
for data and ignore stderr unless you are diagnosing a failure.

## Input value convention

`--schema`, `--instructions`, and `--data` each accept one of three forms:

- a **literal string**: `--schema '{"type":"object"}'`
- **`@file`**: `--schema @schema.json` (reads the file)
- **`-`**: `--schema -` (reads stdin; only one flag per call may use `-`)

JSON-valued flags are validated locally before the request; invalid JSON fails
with exit `2` and no network call.

**Schema shape hint.** When a `--schema` value is a JSON object carrying none of
`type`, `properties`, `$ref`, `allOf`, `anyOf`, `oneOf`, `not`, `enum`, `const`,
`items`, `prefixItems`, `$defs`, `definitions`, `patternProperties`, or
`additionalProperties`, a hint is written to **stderr** and the request proceeds
unchanged. It is advisory only: it never alters stdout, the response, or the
exit code, so do not treat its presence as a failure. It fires on `--schema` and
`--schema-name`, never on `--data`.

## Commands

### `extract markdown <url>...`: page → clean Markdown

Non-streaming. Single JSON response, or NDJSON for a batch (see **Batches**).

| Flag | Required | Notes |
|------|----------|-------|
| `--effort min\|standard\|max` | no | Fetch effort, default `standard`. See table below. |
| `--geo <CC>` | no | ISO 3166-1 alpha-2 country, e.g. `GB`. Format is checked locally (two ASCII letters, any case); a bad value exits `2` with no request. An empty value means no geotargeting. |
| `--metadata` | no | Include page metadata (title, author, …) in the response. |
| `--raw` | no | Print **only** `content`, no envelope, no header, in either mode. Mutually exclusive with `--metadata` (exit `2`), and with several URLs unless `--output-dir` is set. |
| `--no-cache` | no | Bypass cache, fetch fresh. |

Output (`-o json`):
```json
{"content":"# Title…","url":"https://…","metadata":{"title":"…","author":"…"}}
```
`metadata` is present only when `--metadata` was passed.

With `--raw` there is **no JSON envelope in either mode**: stdout is the Markdown
body followed by exactly one newline. Use it when writing a file; use the
envelope when you need `url` or `metadata` alongside the content.

Example:
```bash
tabstack -o json extract markdown https://example.com --metadata
tabstack extract markdown https://example.com --raw > page.md
```

### `extract json <url>... --schema …`: page → schema-shaped JSON

Non-streaming. **The response is exactly your schema's shape**, returned verbatim.

| Flag | Required | Notes |
|------|----------|-------|
| `--schema` | one of | JSON schema (literal / `@file` / `-`). Must be valid JSON. Describes a **shape**, not example values. |
| `--schema-name` | one of | A schema pulled with `tabstack schema pull`, by bare name or repo path. |
| `--storage <dir>` | no | Where `--schema-name` looks (default: config dir). |
| `--effort` / `--geo` / `--no-cache` | no | As above. |

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
| `--schema` | **yes** | Output JSON schema (literal / `@file` / `-`). |
| `--effort` / `--geo` / `--no-cache` | no | As above. |

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
| `--max-duration <dur>` | no | Bound the **whole stream**, e.g. `10m`. Unset by default. On expiry: exit `1`. |

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
| `--max-duration <dur>` | no | Bound the **whole stream**, e.g. `2m`. Unset by default. On expiry: exit `1`. |
| `--fetch-timeout <sec>` | no | Per-page fetch timeout in seconds (integer). |
| `--no-cache` | no | Force fresh research. |

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

### `auth login` / `auth status`

- `auth login`: interactive (prompts for a hidden key) unless `--key <key>` is
  passed; saves to the config file. **Agents should prefer `TABSTACK_API_KEY`**
  and skip this entirely.
- `auth status`: reports whether a key is configured and its source; never
  prints the key. Works without a key present.

## Batches (`extract markdown`, `extract json`)

Both accept several URLs, or `-` to read a newline-delimited list from stdin
(blank lines and `#` comments skipped, duplicates dropped).

| Flag | Default | Notes |
|------|---------|-------|
| `--concurrency <n>` | `4` | URLs in flight at once. |
| `--output-dir <dir>` | unset | One file per URL. Name is `<host-and-path>-<sha8>.<md\|json>`, a pure function of the URL. Existing files refused unless `--force`. |
| `--batch` | off | Force the envelope for a single URL, so the shape is stable. |
| `--force` | off | Overwrite files in `--output-dir`. |

**Output shape depends on the URL count**, so pass `--batch` if you want one
shape unconditionally:

- **one URL, no `--batch`**: exactly the single-result shape documented above.
- **more than one, or `--batch`**: NDJSON, one envelope per line, in **input
  order** regardless of completion order:
  ```json
  {"url":"https://a.com","ok":true,"result":{…}}
  {"url":"https://b.com","ok":false,"error":{"code":3,"message":"api error (404): Not Found"}}
  ```
  `error.code` is the exit code that failure would have produced alone, so an
  API rejection (`3`) is distinguishable from a network problem (`1`) without
  parsing the message.

**Exit codes for a batch**: `0` only if every URL succeeded, otherwise `3`.
Successful results are still emitted and still written. A local problem (bad
URL, `-` competing with `--schema -`, `--raw` with several URLs and no
`--output-dir`) is `2` and happens before any request. All URLs are validated
up front, so a batch never fails partway with `2`.

**There is no `--continue-on-error`**: continuing is the default and the only
behaviour. Read the per-URL `ok` field to find what failed.

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
| `1` | runtime / network error | already retried twice; check connectivity, or raise `--retries`. |
| `2` | usage / invalid input or missing config | **fix the command**: bad flag, missing required arg, malformed JSON, out-of-range value, or no API key configured. Do not retry unchanged. |
| `3` | API error or in-band task failure | inspect the error message / failed event; the request reached the API but was rejected or the task failed. Adjust the request (URL, task wording, schema) before retrying. |

Exit-3 messages keep the literal `api error (NNN): <message>` core, so existing
matches on that substring still work. Three statuses append actionable guidance,
and every response carrying an `x-trace-id` appends `(trace id <id>)`:

| Status | Appended guidance |
|--------|-------------------|
| `401` | key may be revoked or expired; run `tabstack auth login` or check `tabstack auth status` |
| `403` | key may belong to a different organisation; check `tabstack auth status` and `--org` |
| `429` | rate limited; includes `retry after <n>s` when the server sent `Retry-After` |

Quote the trace id in a support request. It is now on the failure itself, so
`--debug` is no longer needed to obtain it.

Errors in `-o json` mode are written to **stderr** as `{"error":"<message>"}`.

## Gotchas

- **`extract json` / `generate json` never wrap the result.** You get exactly the
  shape your schema defines. Don't expect an envelope.
- **`extract markdown` does wrap the result**, unlike the two above: `-o json`
  gives `{"content":…}`, so a bare `> page.md` writes JSON. Pass `--raw` for the
  Markdown alone, or read `.content` from the envelope.
- **A 4xx rejection (typically 400) means the input was unprocessable** (bad URL, schema, or
  task): exit `3`. Retrying with higher `--effort` will **not** fix it; fix the
  input. Higher effort/`--no-cache` only helps transient fetch problems.
- **Streaming output is NDJSON, not a single JSON document.** Parse per line.
- **Transient failures are already retried** (408, 409, 429, 5xx; twice by
  default, exponential backoff with jitter, honouring `Retry-After`). By the
  time you see a non-zero exit the retries are spent, so **do not add your own
  retry loop on top** without lowering `--retries` first. `400` and `404` are
  never retried: the request is wrong, fix it. Retries share the `--timeout`
  deadline and cannot extend it.
- **Streaming commands retry only stream establishment.** Once `automate` or
  `research` starts emitting events it is never replayed, so a mid-stream
  failure is final.
- **Retry lines go to stderr and are suppressed under `-o json`** unless
  `--debug` is set, so they never appear in your NDJSON.
- **`--timeout` does not apply to `automate`/`research`** (a hard timeout would
  cut the stream). Non-streaming calls default to a 2-minute deadline; pass
  `--timeout 0` to disable it. To bound a stream use **`--max-duration`**, which
  exists for exactly this and exits `1` on expiry naming the elapsed time. In CI
  always set one, or a stalled run burns the whole job timeout.
- **`SIGINT` and `SIGTERM` cancel the in-flight request** rather than killing
  the process, so the server is told to stop. The CLI prints `cancelled` to
  stderr and exits `1`. That is a plain line, not the usual styled error block,
  so do not parse it as a failure message.
- **Irreversible remote actions confirm.** `keys revoke`, `auth logout --all`,
  and `auth sessions --revoke` prompt on a terminal and **exit `2` without one**.
  Always pass `--yes` when driving them non-interactively.
- **`agent input` is unreachable without `--interactive`** on the original run.
- **One stdin per call.** At most one `-` flag per invocation.
