# stalewood — contributor guide

`stalewood` is a small single-package Go CLI. Its command-line behaviour
follows the [Command Line Interface Guidelines](https://clig.dev) where
reasonable. When changing CLI behaviour, keep to the rules below.

## Output streams

- Primary output (the report, JSON) goes to **stdout**.
- Errors, diagnostics and progress — anything that is not the result —
  go to **stderr**.
- A caller must be able to pipe stdout into another tool without noise.

## Exit codes

- `0` — success.
- `1` — runtime failure (a scan or removal operation failed).
- `2` — usage error (unknown flag, bad or extra argument).

## Help & version

- `-h` / `--help` prints help to **stdout** and exits `0`; it leads with
  examples.
- A flag/argument error prints the message + usage to **stderr**, exits `2`.
- `--version` prints `stalewood <version>` to stdout and exits `0`.

## Flags & arguments

- Flag names are full words and all work with `--` (e.g. `--prune`); show
  the `--` form in help.
- Exactly one optional positional operand: the path to scan. Everything
  else is a flag.
- Running with no flags must be safe and read-only.

## Destructive actions

- Anything that deletes (`--prune`) is opt-in via an explicit flag, never
  the default.
- `--prune --dry-run` and the default report both show what would be
  removed without removing it.
- Skip dirty/locked worktrees unless `--force` is given; never remove
  abandoned worktrees automatically.
- No interactive confirmation prompt — the explicit `--prune` flag plus
  dry-run is the safeguard.

## Report layout

The human report is a tree grouped by repo: a `●` repo node (full repo
path), each worktree as a tree node with a glyph + verdict + tags, and
field leaves (path, branch, base, or fix/error). A summary and a
present-only legend follow. JSON (`--json`) is grouped by repo, mirroring
the tree, and prints no progress or verbose output — pure machine output.
`--json-schema` prints the JSON Schema for that output; `schema.go` holds
it and `TestJSONSchema` guards it against drift.

## Status indicators

Keep the vocabulary small; every glyph and tag must be a signal a reader
acts on.

- Glyphs: `✓` merged, `✗` unmerged, `⚠` abandoned, `!` error.
- `-> REF` — merged into a branch other than the worktree's own base.
- `[claude]` — created by Claude Code (under `.claude/worktrees/`);
  manual worktrees carry no tag.
- `[modified files]` / `[untracked files]` — tracked changes / untracked
  files present.
- `[locked]` / `[lock-stale]` — a git worktree lock is held; `lock-stale`
  when the PID named in the lock reason is no longer running.
- `[git-prunable]` — git's own `worktree list` flags the entry prunable.

The legend prints only the glyphs and tags that appear in the report.

## Lint mode

`--lint` is a separate, repo-scoped fast path (no directory-tree walk) for
git hooks: it checks the one repo containing the path and exits 1 if any
worktree matches the selector, 0 if none, 2 on a bad selector. Predicates
live in `lint.go` as pure functions of Worktree state. Selector grammar:
comma = AND within one `--lint` value, a repeated `--lint` = OR, `!` = NOT.

## Terminal-aware behaviour (colour, progress, paging)

- Detect a terminal with `isTTY`. All of the below degrade to plain,
  unpaged, uncoloured output when stdout/stderr is not a terminal.
- **Colour & weight**: glyph and verdict are bold + colour-coded by
  severity; repo nodes bold-cyan; connectors and field labels dim; tags
  coloured by kind. Only when stdout is a TTY and `NO_COLOR` is unset.
- **Progress**: a transient stderr line, shown only on an interactive
  stderr and not under `--verbose`/`--quiet`.
- **`--verbose`** logs durable per-worktree detail to stderr; **`--quiet`**
  silences progress.
- **Paging**: human output is paged through `$PAGER` (default
  `less -FIRX`) on an interactive stdout, unless `--print` / `--no-pager`.
  JSON is never paged.

## Robustness

- Every git subprocess runs under a timeout (`gitTimeout`) so a wedged
  repo becomes an error node instead of hanging the whole scan.
- `pidAlive` is build-tagged per OS (`pid_unix.go`, `pid_windows.go`) so
  the tool cross-compiles to every supported target.

## Releasing

Tag `vX.Y.Z` and push the tag; `.github/workflows/release.yml` runs
GoReleaser (`.goreleaser.yaml`), which cross-builds, publishes a GitHub
Release, and updates the Homebrew formula in `retif/homebrew-tap`. The
`version` var is stamped via ldflags; plain `go install` builds fall back
to the module version. `just dist` builds a local snapshot.

## Security & provenance

- CI runs CodeQL, govulncheck and the OpenSSF Scorecard; Dependabot keeps
  the GitHub Actions versions current.
- Releases carry a signed SLSA build-provenance attestation, a keyless
  cosign signature over `checksums.txt`, and a syft SBOM per archive.
- Keep all of this CI-only — no third-party scanning accounts and no
  long-lived secrets.

## Keep it boring

- The standard-library `flag` package is sufficient; do not add a CLI
  framework.
- Stay dependency-free (the Nix build relies on `vendorHash = null`).
- Flag and output changes must be additive — do not break existing
  invocations or the JSON schema.
- `just check` (gofmt + vet + test) must pass before committing.
