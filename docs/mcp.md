# mcp

```
tabstack mcp [--flags]
```

Runs a local [Model Context Protocol](https://modelcontextprotocol.io) server
over **stdio**, exposing the product API as tools an MCP client (Claude
Desktop, an IDE) can call. Built on the official Go SDK; each tool is a thin
adapter over the same client the CLI itself uses.

An MCP client normally launches this for you rather than you running it by
hand.

## Tools

| Tool | Kind | Credential |
|---|---|---|
| `extract_markdown`, `extract_json`, `generate_json` | request/response | API key |
| `automate`, `research` | streaming | API key |
| `schema_list`, `schema_resolve` | local store, read-only | none |
| `whoami`, `list_orgs`, `active_org` | read-only | session |

Streaming tools forward each event as an MCP progress notification when the
client sends a progress token, and aggregate the final answer plus in-band
success or failure into the result.

`schema_list` and `schema_resolve` read the local store and never the network;
pulling schemas stays a CLI operation.

A tool returning an error becomes an MCP tool error (`isError`), never a
process exit, so one bad call cannot kill the server.

## Credentials

Product tools use the org-scoped API key, resolved exactly as for every other
command. Management tools use the session, with automatic refresh and rotation.
The host and credential split is preserved: the key never reaches the console
host and the session never reaches the product host.

If no key is stored but you are signed in with an active organisation, one is
created non-interactively at startup and saved, so the next start skips that.
With neither a key nor a session, the server exits `2` pointing at
`tabstack auth login`.

## Flags

| Flag | Meaning |
|---|---|
| `--api-key`, `--base-url`, `--timeout` | as for the product commands |
| `--auth-url` | console host, for the management tools |
| `--debug` | per-call request id, timing, and rate limits, to stderr |

`--org` is deliberately absent: the server resolves its key without a one-shot
override.

## The stdio invariant

**stdout carries JSON-RPC frames only.** All diagnostics, including `--debug`,
go to stderr, and there is no interactive prompt. A closed stdin or a signal is
a clean shutdown, not a failure exit.

## Setup

```bash
tabstack auth login   # or set TABSTACK_API_KEY for a non-interactive setup
```

Claude Desktop or IDE config entry:

```json
{
  "mcpServers": {
    "tabstack": { "command": "tabstack", "args": ["mcp"] }
  }
}
```

For a non-interactive setup, add the key to the entry:

```json
{
  "mcpServers": {
    "tabstack": {
      "command": "tabstack",
      "args": ["mcp"],
      "env": { "TABSTACK_API_KEY": "ts-..." }
    }
  }
}
```
