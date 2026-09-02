# tabstack command reference

One page per command group, each documenting every subcommand: what it does,
its arguments and flags, worked examples, and the shape of its `--output json`.

The same material is in `tabstack <command> --help`; these pages exist so you
can read across commands, link to a specific one, and see the JSON shapes
written down rather than inferred from a sample.

The hosted documentation lives at <https://docs.tabstack.ai>. These pages track
the code in this repository, so where the two disagree, these are current.

| Page | Commands | Talks to |
|---|---|---|
| [extract](extract.md) | `extract markdown`, `extract json` | product API |
| [generate](generate.md) | `generate json` | product API |
| [agent](agent.md) | `agent automate`, `agent research`, `agent input` | product API |
| [schema](schema.md) | `schema list`, `pull`, `status`, `path`, `rm` | GitHub, local disk |
| [auth](auth.md) | `auth login`, `status`, `switch`, `logout`, `sessions` | console |
| [keys](keys.md) | `keys create`, `list`, `use`, `revoke` | console |
| [config](config.md) | `config show`, `path`, `drop-legacy-key` | local disk |
| [mcp](mcp.md) | `mcp` | both |

`cli-page.md` sits alongside these but is **not part of this reference**: it is
a drop-in replacement for the published page at
`docs.tabstack.ai/getting-started/cli`, which lives in another repository.

## Conventions used on these pages

**Two credentials.** A **session** is user scoped, created by `auth login`, and
sent only to the console host. An **API key** is organisation scoped, created by
`keys create`, and is what product commands send. They are never
interchangeable. Which one a command uses is listed in the table above.

**Key resolution order**, highest first: `--api-key` (or `--key`) →
`TABSTACK_API_KEY` → the stored key for a `--org` override → the active
organisation's stored key → a pre-organisation key, only while no org is active.

**Output.** Pretty on a terminal, JSON when piped. `--output` forces either.
Streaming commands emit NDJSON, one event per line. **stdout carries results;
progress, prompts, and warnings go to stderr**, so a pipe only ever receives
data.

**Exit codes.** `0` success, `1` runtime or network error, `2` usage error or
missing configuration, `3` the API rejected the request or a streamed task
failed in-band.

**Common flags.** These are not repeated in every table below:

| Flag | Available on | Meaning |
|---|---|---|
| `-o, --output pretty\|json` | everything | force an output mode |
| `--no-color` | everything | disable colour (or set `NO_COLOR`) |
| `--api-key`, `--key` | `extract`, `generate`, `agent`, `mcp` | override the product credential |
| `--base-url` | `extract`, `generate`, `agent`, `mcp`, `config` | product API host |
| `--auth-url` | `auth`, `keys`, `config`, `mcp` | console host |
| `--timeout <dur>` | `extract`, `generate`, `agent`, `mcp` | non-streaming deadline, default `2m`, `0` disables |
| `--retries <n>` | `extract`, `generate`, `agent`, `mcp` | retry 408/409/429/5xx, default `2`, `0` disables |
| `--org <selector>` | `extract`, `generate`, `agent`, `keys`, `auth login` | act as another organisation, once |
| `--debug` | `extract`, `generate`, `agent`, `mcp` | request id, timing, rate limits to stderr |

A flag only appears on the commands that read it, so passing `--api-key` to
`keys list` is an error rather than a silent no-op.

## Project configuration

A repository may ship a `.tabstack.toml` pinning shared settings. It is found by
searching upwards from the working directory and sits between environment
variables and your own config: **flags > env > project > user config > default**.

It may set `active_org`, `storage`, `output`, `effort`, `geo`, `timeout`,
`max_duration`, `concurrency`, and `retries`, and nothing else. Credentials and
endpoints are rejected with exit `2`, because a project file arrives by
`git clone` and must not be able to carry a key or redirect where yours is sent.

## Environment

| Variable | Effect |
|---|---|
| `TABSTACK_API_KEY` | API key for product calls; overrides every stored key |
| `TABSTACK_BASE_URL` | product API host (default `https://api.tabstack.ai/v1`) |
| `TABSTACK_AUTH_URL` | console host (default `https://console.tabstack.ai`) |
| `TABSTACK_OAUTH_SCOPES` | override the scopes requested at login |
| `NO_COLOR` | disable coloured output |
| `XDG_CONFIG_HOME` | where `config.toml` and pulled schemas live |
| `TABSTACK_NO_PROJECT_CONFIG` | ignore any project `.tabstack.toml` |
| `TABSTACK_PROJECT_CONFIG` | use this project file, skipping discovery |
