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
tabstack extract markdown <url>... [--flags]
```

Converts a page to clean Markdown, stripping navigation, boilerplate, and
markup noise.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<url>...` | yes | one or more pages to fetch; each must be `http` or `https`, all validated before any request. `-` reads a newline-delimited list from stdin. See [Batches](#batches). |

### Flags

| Flag | Meaning |
|---|---|
| `--metadata` | include the page's title, author, site name, and similar |
| `--raw` | print only the Markdown body: no header, no styling, no JSON envelope, in either mode |
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

# Save the Markdown itself to a file
tabstack extract markdown https://example.com --raw > page.md

# The same, without the flag, by reading the field out of the envelope
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

### `--raw`

Output mode auto-detects, so a plain `> page.md` is not a terminal and writes
the envelope above into a file named `.md`. `--raw` is the escape hatch: it
prints `content` and nothing else, **ignoring the output mode**, with exactly
one trailing newline so redirects and `$(...)` capture both behave.

```bash
tabstack extract markdown https://example.com --raw > page.md
tabstack extract markdown https://example.com --raw | pbcopy
```

`--raw` with `--metadata` is a contradiction and fails with exit `2`. `-o pretty`
is not a substitute: it still prepends the styled metadata header. The JSON
envelope is deliberately unchanged, so anything already parsing it keeps
working.

---

## extract json

```
tabstack extract json <url>... [--flags]
```

Extracts data from a page into the shape described by a JSON schema. The
response is whatever your schema describes, so the CLI passes it through
verbatim and never reshapes it.

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<url>...` | yes | one or more pages; `-` reads a list from stdin. See [Batches](#batches). |

### Flags

| Flag | Meaning |
|---|---|
| `--schema <spec>` | the schema: a literal string, `@file`, or `-` for stdin |
| `--schema-name <name>` | instead, use a schema pulled with `schema pull` (bare name or full repo path) |
| `--storage <dir>` | where to look for `--schema-name` (default: the config directory) |
| `--effort`, `--geo`, `--no-cache` | as for `extract markdown` |

A schema describes the **shape** you want, not the values: `{"title":"string"}`
is example data, `{"type":"object","properties":{"title":{"type":"string"}}}` is
a schema. A schema with no shape keyword gets a hint on stderr and is sent
anyway, since the server is the validator.

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

---

## Batches

Both commands accept several URLs, or `-` to read a newline-delimited list from
stdin. Blank lines and `#` comments are skipped, so a list can be checked into a
repo. Duplicate URLs are fetched once.

```bash
tabstack extract markdown https://a.com https://b.com
tabstack extract json - --schema-name job-posting < urls.txt
tabstack extract markdown https://first.com - https://last.com < middle.txt
```

Only one thing per invocation may read stdin, so `extract json - --schema -`
fails with exit `2` naming the conflict.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--concurrency <n>` | `4` | how many URLs are fetched at once |
| `--output-dir <dir>` | unset | write one file per URL instead of printing |
| `--batch` | off | use the envelope even for a single URL |
| `--force` | off | overwrite existing files in `--output-dir` |

### Output

**One URL and no batch flags: unchanged**, exactly as documented above, so
existing scripts keep working.

**More than one URL, or `--batch`:** NDJSON, one envelope per line, in **input
order** whatever order they finish in, so the output is diffable:

```json
{"url":"https://a.com","ok":true,"result":{"content":"# A","url":"https://a.com"}}
{"url":"https://b.com","ok":false,"error":{"code":3,"message":"api error (404): Not Found"}}
```

`error.code` is the exit code that failure would have carried alone. Pretty mode
prints a styled URL header before each result, with failures inline.

### Files

`--output-dir` writes each result as it completes. The filename is
`<host-and-path>-<sha8>.<ext>`, for example:

```
https://example.com/blog/post-1  ->  example.com_blog_post-1-b2117d83.md
https://example.com/s?q=a        ->  example.com_s-d40dbe50.json
```

The hash is always present, so a name is a pure function of its URL. Adding a
URL to the list never renames the others, which makes repeat runs idempotent.
An existing file is refused unless `--force` is given.

`--raw` with more than one URL and no `--output-dir` is exit `2`: the documents
would run together on stdout with nothing to separate them.

### Exit codes

`0` only when every URL succeeded, otherwise `3`, with successful results still
emitted and written and a per-URL summary on stderr. Local problems (a malformed
URL, a stdin conflict) are exit `2` and happen before any request, so a batch
never fails partway through with `2`.

Continuing past a failure is the default and the only behaviour; read the `ok`
field to find what failed.
