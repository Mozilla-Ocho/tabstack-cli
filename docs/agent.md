# agent

Long-running AI tasks that stream their progress. Both `automate` and
`research` are **Server-Sent Events** calls: the CLI prints each event as it
arrives and the final answer at the end.

Because a hard deadline would cut a stream mid-flight, **`--timeout` does not
apply to these two**. To bound a stream use **`--max-duration`**, which stops
the whole run and exits `1` naming the elapsed time and the flag. `agent input`
is an ordinary request/response call, so `--timeout` applies to it and
`--max-duration` does not exist there.

Ctrl-C (or `SIGTERM`) cancels the request in flight, so the server is told to
stop rather than the process being killed mid-call. The CLI prints `cancelled`
to stderr and exits `1`.

Failure is signalled **in-band**, not by HTTP status: a `task:completed` event
carrying `success:false`, or an `error` event. Both map to exit code `3`, so
scripts can branch on the exit status as usual.

- [`agent automate`](#agent-automate)
- [`agent research`](#agent-research)
- [`agent input`](#agent-input)

---

## agent automate

```
tabstack agent automate <task> [--flags]
```

Runs a browser-automation task described in natural language, server-side.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<task>` | yes | what to do, in plain language; quote it |

### Flags

| Flag | Meaning |
|---|---|
| `--url <url>` | starting URL for the task; validated locally when given |
| `--data <spec>` | JSON context for form filling or complex workflows: literal, `@file`, or `-` |
| `--guardrails <text>` | safety constraints on what the task may do |
| `--interactive` | allow the task to pause and ask for input, answered with `agent input` |
| `--max-iterations <n>` | cap on task iterations, 1 to 100, checked locally |
| `--max-validation-attempts <n>` | cap on validation attempts, 1 to 10, checked locally |
| `--geo <CC>` | run via an exit node in this country |
| `--max-duration <dur>` | stop the whole stream after this long, e.g. `10m`; unset by default |

### Examples

```bash
# Run a task, streaming progress as it works
tabstack agent automate "find the pricing page and list the plans" --url https://example.com

# Supply context for a form
tabstack agent automate "fill in the contact form" --url https://example.com/contact \
  --data '{"name":"Ada","email":"ada@example.com"}'

# Let the task pause to ask you something mid-run
tabstack agent automate "book the cheapest flight" --url https://example.com --interactive

# Constrain how hard it tries
tabstack agent automate "find the careers page" --url https://example.com --max-iterations 10
```

### Output

Pretty mode renders each event as a labelled line, with a spinner between
events (only on a real terminal), then the final answer. JSON mode emits
**NDJSON**, one event per line:

```json
{"event":"task:started","data":{"...":"..."}}
{"event":"navigate","data":{"url":"https://example.com/pricing"}}
{"event":"task:completed","data":{"success":true,"answer":"..."}}
```

---

## agent research

```
tabstack agent research <query> [--flags]
```

Searches the web, reads sources, and synthesises an answer with numbered
citations.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<query>` | yes | the question; quote it |

### Flags

| Flag | Meaning |
|---|---|
| `--mode fast\|balanced` | `fast` (default) for quick answers, `balanced` for deeper multi-source work |
| `--fetch-timeout <n>` | per-page fetch timeout, in seconds |
| `--no-cache` | skip the cache and research fresh |
| `--max-duration <dur>` | stop the whole stream after this long, e.g. `2m`; unset by default |

### Examples

```bash
# Quick answer with cited sources
tabstack agent research "what changed in HTTP/3 in 2024?"

# Deeper multi-source research
tabstack agent research "compare managed Postgres pricing" --mode balanced

# Machine-readable: one JSON event per line
tabstack agent research "who maintains curl?" --output json | jq -c .event
```

### Output

Pretty mode streams the phases, then prints the report followed by its cited
sources. JSON mode emits NDJSON in the same `{"event":...,"data":...}` shape as
`automate`.

---

## agent input

```
tabstack agent input <request-id> [--flags]
```

Answers an automation that paused to ask for something. The request id comes
from the `automate` stream, and only tasks started with `--interactive` can
pause.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<request-id>` | yes | the id from the pausing event |

### Flags

| Flag | Required | Meaning |
|---|---|---|
| `--data <spec>` | yes | the response, as a JSON object: literal, `@file`, or `-` |

The payload must set either `fields` or `cancelled`; anything else is rejected
locally, since unknown keys are ignored by the API and would look like a
silent success.

### Examples

```bash
# Provide values
tabstack agent input req_abc123 --data '{"fields":[{"ref":"email","value":"ada@example.com"}]}'

# Decline the request instead
tabstack agent input req_abc123 --data '{"cancelled":true}'
```
