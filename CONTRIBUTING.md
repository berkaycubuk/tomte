# Contributing

Tomte is small on purpose: the core uses only Go's standard library, and
[docs/design.md](docs/design.md) says what it isn't going to be. So:

- **Bugs:** open an issue with what you ran, what happened and what you
  expected. A pull request with a fix and a test is welcome.
- **Features:** open an issue first and say what you'd use it for. Many
  good ideas don't belong in the core and work well as your own channel,
  tool or package beside it. A pull request without an issue may be
  closed.
- **Security:** see [SECURITY.md](SECURITY.md); don't open a public
  issue.

Before sending a pull request:

```sh
go vet ./...
go test -race ./...
```

`shellcheck` checks the scripts in `cmd/tomte/box` when it's installed.
Keep to the style around you: short names, comments that say why, and no
new dependencies.
