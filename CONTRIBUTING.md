# Contributing to stalewood

Thanks for your interest in improving stalewood.

## Workflow

External changes go through pull requests:

1. Fork the repository and create a branch.
2. Make your change — keep it small and focused.
3. Run `just check` (gofmt + vet + tests); it must pass.
4. Open a pull request against `main`.

CI runs the test suite, `go vet`, CodeQL and govulncheck on every pull
request; all must pass, and a maintainer review is required before merge.

## Development

`nix develop` gives a shell with the Go toolchain and tools, or use a local
Go install (see `go.mod` for the version). `just` lists the available tasks.

The project is a single Go package with no third-party dependencies — keep
it that way. See `CLAUDE.md` for the design and CLI conventions.
