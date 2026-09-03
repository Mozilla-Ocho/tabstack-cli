# auth

Manage the **session**: the user-scoped credential created by signing in, sent
only to the console host (`console.tabstack.ai`). API keys are a separate,
organisation-scoped credential and live under [`keys`](keys.md).

The session is refreshed automatically. Refresh is single-flight within a
process, and because the CLI and a running MCP server share one stored session,
a rotation by one is picked up by the other rather than forcing a re-login.

- [`auth login`](#auth-login)
- [`auth status`](#auth-status)
- [`auth switch`](#auth-switch)
- [`auth logout`](#auth-logout)
- [`auth sessions`](#auth-sessions)

---

## auth login

```
tabstack auth login [--flags]
```

Runs an OAuth 2.1 authorization code flow with PKCE. It opens the console in
your browser and waits on a loopback listener at
`http://127.0.0.1:<random-port>/callback`. Nothing is pasted, and no secret
reaches your shell history. If no browser can be opened the printed URL still
works. On a machine with no display at all, login fails and points you at
`TABSTACK_API_KEY`, which is the right credential for CI.

### Flags

| Flag | Meaning |
|---|---|
| `--api-key-setup create\|existing\|skip` | what to do about an API key once signed in |
| `--no-key` | alias for `--api-key-setup=skip` |
| `--org <selector>` | set up the key for this organisation |

`existing` adopts a key the organisation already has, and is only offered once
the CLI has listed them and found at least one; asking for it when there are
none is an error. Combining `--no-key` with a conflicting `--api-key-setup` is
also an error.

### Examples

```bash
tabstack auth login                          # sign in and set up a key
tabstack auth login --no-key                 # CI, or you already have a key
tabstack auth login --api-key-setup=existing # adopt a key instead of minting one
```

---

## auth status

```
tabstack auth status
```

Who you are, which organisation you are acting as, and **which credential would
actually be sent**. If `TABSTACK_API_KEY` is set it says so plainly, rather than
showing a stored key that is not being used. With a session it also checks the
stored key still exists server-side, so a key revoked in the console is caught
here instead of as a surprise 401.

### Output

Pretty mode prints a short report. JSON mode:

```json
{
  "signed_in": true,
  "email": "ada@example.com",
  "session_expires_at": "2026-10-02T19:15:34Z",
  "session_expired": false,
  "active_org": "org_01hxyz",
  "active_org_name": "Acme",
  "api_key_stored": true,
  "api_key_preview": "sk-a…efgh",
  "api_key_name": "cli-laptop",
  "api_key_from_legacy_config": false,
  "env_override": false,
  "config_path": "/home/ada/.config/tabstack/config.toml",
  "config_permissions": "0600",
  "config_permissions_ok": true
}
```

The key is always a redacted preview. No output mode prints it in full.

---

## auth switch

```
tabstack auth switch [organisation]
```

Changes which organisation your commands act as. This is a **credential
change**, not a display setting: it decides which stored API key goes out. It
never signs you in again, because the session is user scoped.

A selector resolves by exact id, then exact name (case-insensitive), then
unique name prefix. An ambiguous prefix lists the matches and fails rather than
guessing. With no argument you get a picker; a user with one organisation is
told so instead of being shown a list of one.

If the console is unreachable but the organisation is already in your config,
the switch proceeds with a warning. An organisation list the server actively
rejected is a hard error.

Switching into an organisation with no stored key runs the same key setup that
login does, so it is not a dead end.

```bash
tabstack auth switch              # pick from a list
tabstack auth switch org_01hxyz   # by id
tabstack auth switch acme         # by name or unique prefix
```

### Output

```json
{"action":"switch","ok":true,"org":"org_01hxyz","org_name":"Acme","api_key_stored":true}
```

---

## auth logout

```
tabstack auth logout [--all] [--yes]
```

Revokes this session and clears it locally. `--all` revokes every session for
your user, on every machine, and **asks first**; pass `--yes` to skip the
prompt. On a non-interactive terminal without `--yes` it refuses with exit `2`
rather than proceeding unasked.

An already-dead session is still cleared locally, so you cannot get stuck with
a stale token and no way to drop it. Stored API keys are left in place;
[`keys revoke`](keys.md#keys-revoke) removes those.

```bash
tabstack auth logout
tabstack auth logout --all --yes
```

### Output

```json
{"action":"logout_all","ok":true}
```

---

## auth sessions

```
tabstack auth sessions [--revoke <id>] [--yes]
```

Lists your CLI sessions, marking the current one. Each is labelled with the
hostname it was created from, so they are tellable apart. `--revoke` kills one
by id, and asks first unless `--yes` is given.

```bash
tabstack auth sessions
tabstack auth sessions --revoke sess_abc123
```

### Output

JSON mode emits the session records:

```json
[{"id":"sess_abc123","label":"laptop","last_used_at":"2026-09-01T10:00:00Z","created_at":"2026-08-01T09:00:00Z","expires_at":"2026-10-01T09:00:00Z","current":true}]
```

Revoking emits `{"action":"session_revoked","ok":true,"id":"sess_abc123"}`.
