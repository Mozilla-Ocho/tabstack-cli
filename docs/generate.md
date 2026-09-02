# generate

Fetch a URL, extract its content, then transform it with AI into the shape you
describe. Where [`extract json`](extract.md#extract-json) pulls out data that is
already on the page, `generate json` produces something new from it: a summary,
a rewrite, a classification.

- [`generate json`](#generate-json)

---

## generate json

```
tabstack generate json <url> [--flags]
```

### Arguments

| Argument | Required | Meaning |
|---|---|---|
| `<url>` | yes | page to fetch; must be `http` or `https`, validated locally |

### Flags

| Flag | Required | Meaning |
|---|---|---|
| `--instructions <spec>` | yes | what to do with the content: a literal string, `@file`, or `-` for stdin |
| `--schema <spec>` | one of | the output schema: literal, `@file`, or `-` |
| `--schema-name <name>` | one of | instead, a schema pulled with `schema pull` |
| `--storage <dir>` | no | where to look for `--schema-name` |
| `--effort min\|standard\|max` | no | how hard to work at fetching the page |
| `--geo <CC>` | no | fetch via an exit node in this country |
| `--no-cache` | no | bypass the cache |

A schema describes the **shape** you want, not the values: `{"title":"string"}`
is example data, `{"type":"object","properties":{"title":{"type":"string"}}}` is
a schema. A schema with no shape keyword gets a hint on stderr and is sent
anyway, since the server is the validator.

Exactly one of `--schema` or `--schema-name`. Only one flag may read stdin per
invocation, so `--schema - --instructions -` is rejected up front. Instructions
are length-checked locally against the server's limit.

### Examples

```bash
# Transform a page into a shape you describe
tabstack generate json https://example.com \
  --instructions "summarise this page in three bullet points" \
  --schema '{"type":"object","properties":{"bullets":{"type":"array","items":{"type":"string"}}}}'

# Long instructions from a file, schema from the library
tabstack generate json https://example.com \
  --instructions @prompt.txt --schema-name news-article
```

### Output

Whatever your schema describes, passed through verbatim. Pretty mode indents;
JSON mode emits the bytes unchanged.

### Exit codes

`2` for a missing or malformed schema, missing `--instructions`, instructions
over the length limit, or both flags reading stdin. `3` if the API rejects it.
