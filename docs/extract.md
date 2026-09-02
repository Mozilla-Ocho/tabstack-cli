# extract

Fetch a URL and return its content, either as clean Markdown or as JSON shaped
by a schema you supply. Both send the organisation-scoped API key to the
product host, and both are single request/response calls, so `--timeout`
applies.

- [`extract markdown`](#extract-markdown)
- [`extract json`](#extract-json)

---

## extract markdown

```
tabstack extract markdown <url> [--flags]
```

Converts a page to clean Markdown, stripping navigation, boilerplate, and
markup noise.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<url>` | yes | page to fetch; must be `http` or `https`, validated locally before any request |

### Flags

| Flag | Meaning |
|---|---|
| `--metadata` | include the page's title, author, site name, and similar |
| `--effort min\|standard\|max` | how hard to work at fetching; validated locally |
| `--geo <CC>` | fetch via an exit node in this country, ISO 3166-1 alpha-2 (e.g. `GB`), validated locally |
| `--no-cache` | bypass the cache and fetch fresh (`--nocache` is a hidden alias) |

Plus the [common flags](README.md#conventions-used-on-these-pages).

### Examples

```bash
# Clean Markdown for a page
tabstack extract markdown https://example.com

# Include the title, author, and other page metadata
tabstack extract markdown https://example.com --metadata

# Skip the cache, fetch via a UK exit node, work harder at it
tabstack extract markdown https://example.com --no-cache --geo GB --effort max

# Save just the Markdown body to a file
tabstack extract markdown https://example.com | jq -r .content > page.md
```

### Output

Pretty mode prints the Markdown, with a short styled header when `--metadata`
is set. JSON mode emits the full response object:

```json
{
  "content": "# Example Domain\n\nThis domain is for use in...",
  "url": "https://example.com",
  "metadata": { "title": "Example Domain", "site_name": "example.com" }
}
```

`metadata` is present only with `--metadata`.

---

## extract json

```
tabstack extract json <url> [--flags]
```

Extracts data from a page into the shape described by a JSON schema. The
response is whatever your schema describes, so the CLI passes it through
verbatim and never reshapes it.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<url>` | yes | page to fetch; must be `http` or `https` |

### Flags

| Flag | Meaning |
|---|---|
| `--schema <spec>` | the schema: a literal string, `@file`, or `-` for stdin |
| `--schema-name <name>` | instead, use a schema pulled with `schema pull` (bare name or full repo path) |
| `--storage <dir>` | where to look for `--schema-name` (default: the config directory) |
| `--effort`, `--geo`, `--no-cache` | as for `extract markdown` |

Exactly one of `--schema` or `--schema-name` is required; passing both is an
error. `--schema` is validated as JSON locally, so a malformed schema fails
immediately rather than as an opaque API 400.

### Examples

```bash
# Inline schema
tabstack extract json https://example.com \
  --schema '{"type":"object","properties":{"title":{"type":"string"}}}'

# Schema from a file, or from stdin
tabstack extract json https://example.com --schema @schema.json
cat schema.json | tabstack extract json https://example.com --schema -

# A schema you pulled from the library
tabstack schema pull job-posting
tabstack extract json https://example.com/jobs/1 --schema-name job-posting
```

### Output

Whatever your schema describes. Pretty mode indents it; JSON mode passes the
bytes through unchanged, so the shape is exactly what you asked for.

### Exit codes

`2` if the schema is missing, malformed, names an unpulled schema, or both
schema flags are given. `3` if the API rejects the request. See the
[full list](README.md#conventions-used-on-these-pages).
