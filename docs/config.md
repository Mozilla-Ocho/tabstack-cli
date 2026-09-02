# config

Inspect and tidy the local configuration. Nothing here calls the product API,
and **no command in this group prints a secret in full**: keys and tokens are
redacted to their first and last four characters, in both pretty and JSON
output. There is no flag to override that.

The config lives at `$XDG_CONFIG_HOME/tabstack/config.toml` (usually
`~/.config/tabstack/config.toml`). Writes are atomic and the file is created
`0600`; a group- or world-readable file produces a warning rather than a
refusal.

**Migration** from a pre-organisation config happens automatically on the next
save and is lossless: the old top-level `api_key` becomes `legacy_key`, and is
only used while no organisation is active. There is deliberately no
`config migrate` command, because it would be a no-op nobody knows when to run.

- [`config show`](#config-show)
- [`config path`](#config-path)
- [`config drop-legacy-key`](#config-drop-legacy-key)

---

## config show

```
tabstack config show
```

Prints **every** organisation the CLI knows about and the state of its key, not
just the active one: this is the view that answers "which credential would go
out if I switched". Output is safe to paste into a bug report.

Before the file exists, permissions read `not created yet` rather than a
meaningless `0`.

```bash
tabstack config show
tabstack config show --output json | jq .orgs
```

### Output

```json
{
  "path": "/home/ada/.config/tabstack/config.toml",
  "permissions": "0600",
  "exists": true,
  "permissions_ok": true,
  "version": 1,
  "session": {
    "email": "ada@example.com",
    "access_token_preview": "at-a…efgh",
    "expires_at": "2026-10-02T19:15:34Z",
    "expired": false
  },
  "base_url": "https://api.tabstack.ai/v1",
  "auth_url": "https://console.tabstack.ai",
  "active_org": "org_01hxyz",
  "orgs": [
    {"id":"org_01hxyz","name":"Acme","active":true,"api_key_stored":true,"api_key_preview":"ts-a…efgh","api_key_name":"cli-laptop","api_key_id":"key_abc123"}
  ],
  "legacy_key_in_use": false,
  "env_override": false
}
```

The refresh token has no field at all, redacted or otherwise.

---

## config path

```
tabstack config path
```

Prints the config file path. Pretty mode prints it bare and unstyled so it
substitutes cleanly into other commands; JSON mode wraps it in an object like
everything else.

```bash
tabstack config path
cat "$(tabstack config path)"
```

```json
{"path":"/home/ada/.config/tabstack/config.toml"}
```

---

## config drop-legacy-key

```
tabstack config drop-legacy-key [--force]
```

Removes the single global API key carried over from a pre-organisation config.

It **refuses unless the active organisation has a key of its own**, so it
cannot strand you without a working credential. `--force` overrides that, and
may leave you with nothing.

```bash
tabstack config drop-legacy-key
tabstack config drop-legacy-key --force
```

```json
{"action":"drop_legacy_key","ok":true,"org":"org_01hxyz"}
```
