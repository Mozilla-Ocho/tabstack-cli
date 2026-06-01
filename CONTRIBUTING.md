# Contributing

Thanks for your interest in improving the `tabstack-cli`. This project follows the
[Mozilla Community Participation Guidelines](CODE_OF_CONDUCT.md).

## Getting started

```bash
git clone https://github.com/Mozilla-Ocho/tabstack-cli.git
cd tabstack-cli
make build      # -> ./bin/tabstack
make test
```

You need a recent Go toolchain; the required version is pinned in
[`go.mod`](go.mod). See [CLAUDE.md](CLAUDE.md) for an architecture overview.

## Development workflow

```bash
make lint       # gofmt -w . && go vet ./...
make test       # go test ./...
make smoke      # live API smoke test (needs an API key)
```

Before opening a pull request:

- Run `make lint` and `make test` — CI runs the same checks (`gofmt`, `go vet`,
  `go build`, `go test -race`) and must pass.
- Add or update tests for behaviour you change. Network code is testable via
  `client.WithHTTPClient` (inject an `httptest` server) — no live calls in unit
  tests.
- Keep commits focused and write a clear description of the *why*.

## Pull requests

1. Fork and branch from `main`.
2. Make your change with tests.
3. Ensure `make lint test` is clean.
4. Open the PR and fill in the template.

## Reporting bugs and requesting features

Use the issue templates. For security issues, follow [SECURITY.md](SECURITY.md)
instead of opening a public issue.
