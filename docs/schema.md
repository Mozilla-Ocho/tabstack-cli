# schema

Work with the public [`tabstack-schemas`](https://github.com/Mozilla-Ocho/tabstack-schemas)
library: browse it, pull schemas into a local store, and keep track of what you
have changed. Pulled schemas feed
[`extract json --schema-name`](extract.md#extract-json) and
[`generate json --schema-name`](generate.md#generate-json).

These commands **never send your API key**. They talk to
`raw.githubusercontent.com` unauthenticated and to your local disk, which is
why none of them accept `--api-key`, `--base-url`, or `--timeout`.

**The store** defaults to `$XDG_CONFIG_HOME/tabstack/schemas` (usually
`~/.config/tabstack/schemas`). Every command takes `--storage <dir>` to use a
different one, which is how you keep a project-local set. The on-disk layout
mirrors the repo, so `job-posting` lives at `<store>/jobs/job-posting.json`.

Two bookkeeping files live in the store and are never listed as schemas:
`.manifest.json` records the canonical hash of each schema as pulled, and
`.index-cache.json` caches the library index for an hour.

**A selector** is a bare name (`job-posting`), a category (`jobs`), or a full
repo path (`jobs/job-posting.json`).

- [`schema list`](#schema-list)
- [`schema pull`](#schema-pull)
- [`schema status`](#schema-status)
- [`schema path`](#schema-path)
- [`schema rm`](#schema-rm)

---

## schema list

```
tabstack schema list [--flags]
```

### Flags

| Flag | Meaning |
|---|---|
| `--local` | list only what you have pulled, without touching the network |
| `--refresh` | ignore the cached index and refetch it |
| `--storage <dir>` | which store to read |

### Examples

```bash
tabstack schema list           # the whole library, grouped by category
tabstack schema list --local   # only what you have pulled, offline
tabstack schema list --refresh # bypass the 1-hour index cache
```

### Output

Pretty mode groups by category and marks pulled schemas. JSON mode emits an
array of objects. The library listing carries the full entry:

```json
[{"category":"jobs","title":"Job Posting","description":"...","path":"jobs/job-posting.json"}]
```

`--local` emits the same `path` key, so `jq '.[].path'` works against either,
but without `title` and `description`, which only exist in the online index:

```json
[{"path":"jobs/job-posting.json","name":"job-posting"}]
```

---

## schema pull

```
tabstack schema pull [selector...] [--flags]
```

Downloads schemas into the local store. Re-pulling compares **canonicalised**
JSON, so reformatting alone is not treated as a change.

### Flags

| Flag | Meaning |
|---|---|
| `--all` | pull every schema in the library |
| `--force` | overwrite local edits without asking |
| `--refresh` | bypass the cached index |
| `--storage <dir>` | where to write |

At least one selector, or `--all`, is required.

### Conflicts

When a local file differs from the remote, on a terminal you are prompted to
**[o]verwrite, [k]eep, or [q]uit**; quitting stops there and exits `0`. Without
a terminal, and without `--force`, the command fails with exit `2` rather than
silently clobbering your edits.

`--force` here means "overwrite my local file". It is a different thing from
`--yes`, which confirms an irreversible remote action.

### Examples

```bash
# By bare name, by category, or by full repo path
tabstack schema pull job-posting
tabstack schema pull jobs
tabstack schema pull jobs/job-posting.json

# Everything, overwriting local edits without asking
tabstack schema pull --all --force

# Keep a project-local store instead of the default
tabstack schema pull job-posting --storage ./schemas
```

### Output

Per-schema progress (`✓ pulled ...`, `= up to date ...`) goes to **stderr**, so
a pipe only receives the result. Pretty mode prints a tally to stdout; JSON
mode prints a summary object:

```json
{"pulled":["jobs/job-posting.json"],"up_to_date":[],"kept":[]}
```

---

## schema status

```
tabstack schema status [--flags]
```

Reconciles the store against the manifest and, unless `--local`, against the
current remote.

| State | Meaning |
|---|---|
| `up to date` | matches what was pulled, and matches the remote |
| `modified` | you have edited it since pulling |
| `outdated` | the remote has moved on since you pulled |
| `missing` | recorded in the manifest, but the file is gone |
| `untracked` | a file in the store that was never pulled |

### Examples

```bash
tabstack schema status
tabstack schema status --local   # skip the network; only report local edits
```

### Output

JSON mode emits one object per schema:

```json
[{"path":"jobs/job-posting.json","state":"modified"}]
```

---

## schema path

```
tabstack schema path <name> [--flags]
```

Prints the local file path of a pulled schema, for scripting and for feeding
other tools.

```bash
tabstack schema path job-posting
cat "$(tabstack schema path job-posting)"
```

Pretty mode prints a bare path with no styling, so it substitutes cleanly.

---

## schema rm

```
tabstack schema rm <selector...> [--flags]
```

Deletes pulled schemas and their manifest entries. No confirmation: these are
local files you can re-pull in a second.

```bash
tabstack schema rm job-posting
tabstack schema rm jobs/job-posting.json finance/crypto-asset.json
```

Per-item progress goes to stderr. JSON mode emits:

```json
{"removed":["jobs/job-posting.json"]}
```
