# keys

Manage **API keys**: the organisation-scoped credential that product commands
send to `api.tabstack.ai`. Keys are managed through your signed-in session, so
these commands need [`auth login`](auth.md#auth-login) first, and they take
`--auth-url` rather than `--api-key`.

The CLI stores **one key per organisation** and tracks which is active. Keys it
creates are named `cli-<hostname>` by default, so they are legible when you come
to revoke one in the console.

- [`keys create`](#keys-create)
- [`keys list`](#keys-list)
- [`keys use`](#keys-use)
- [`keys revoke`](#keys-revoke)

---

## keys create

```
tabstack keys create [--name <name>] [--org <selector>]
```

Creates a key for an organisation and stores it. **The plaintext is shown
once**, at creation, and never again: neither `keys list` nor `config show`
will print it.

```bash
tabstack keys create                                # for the active organisation
tabstack keys create --org acme --name cli-laptop   # named, for a specific one
```

### Output

JSON mode includes the plaintext, because this is the one moment the API
returns it and a script creating a key needs it:

```json
{"action":"key_created","ok":true,"id":"key_abc123","name":"cli-laptop","org":"org_01hxyz","api_key":"ts-..."}
```

---

## keys list

```
tabstack keys list [--org <selector>]
```

Lists an organisation's keys as **previews only**. The plaintext is never
printed here, whatever the server includes in the payload. The key this CLI has
stored is marked.

```bash
tabstack keys list
tabstack keys list --org acme
```

### Output

```json
[{"id":"key_abc123","name":"cli-laptop","preview":"ts-a…efgh","org":"org_01hxyz","stored_in_cli":true,"last_used_at":"2026-09-01T10:00:00Z"}]
```

---

## keys use

```
tabstack keys use [key-id] [--org <selector>]
```

Adopts one of the organisation's existing keys and stores it as the one this
CLI sends, replacing whatever was stored. Pass an id from `keys list` to pick
directly; with no id, a single candidate is adopted automatically and multiple
candidates prompt you to choose.

This is the same adoption login offers as `--api-key-setup=existing`, available
on demand and able to replace an already-stored key.

```bash
tabstack keys use              # the only key, or pick from a list
tabstack keys use key_abc123   # a specific one
```

### Output

An acknowledgement with a preview, not a reveal:

```json
{"action":"key_adopted","ok":true,"id":"key_abc123","name":"cli-laptop","org":"org_01hxyz","preview":"ts-a…efgh"}
```

---

## keys revoke

```
tabstack keys revoke <key-id> [--yes]
```

Revokes a key server-side. This **cannot be undone** and breaks everything
still sending that key, so it asks first; `--yes` skips the prompt, and a
non-interactive terminal without `--yes` refuses with exit `2`.

If the revoked key was the one stored for an organisation, it is dropped from
your config too, since leaving it there means sending a dead credential until
the API starts rejecting it. The CLI then tells you that organisation has no
key (on stderr under `--output json`, so the object stays the only thing on
stdout).

```bash
tabstack keys revoke key_abc123
tabstack keys revoke key_abc123 --yes
```

### Output

```json
{"action":"key_revoked","ok":true,"id":"key_abc123"}
```
