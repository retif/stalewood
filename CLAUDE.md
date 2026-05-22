# stalewood — contributor guide

`stalewood` is a small single-package Go CLI. Its command-line behaviour
follows the [Command Line Interface Guidelines](https://clig.dev) where
reasonable. When changing CLI behaviour, keep to the rules below.

## Output streams
- Primary output (the report, JSON) goes to **stdout**.
- Errors, diagnostics, progress — anything that is not the result — goes to **stderr**.
- A caller must be able to pipe stdout into another tool without seeing noise.

## Exit codes
- `0` — success.
- `1` — runtime failure (a scan or removal operation failed).
- `2` — usage error (unknown flag, bad or extra argument).

## Help & version
- `-h` / `--help` prints help to **stdout** and exits `0`; it leads with examples.
- A flag/argument error prints the message + usage to **stderr** and exits `2`.
- `--version` prints `stalewood <version>` to stdout and exits `0`.
- Keep `--help` concise: summary, usage, examples, flags, exit codes.

## Flags & arguments
- Flag names are full words and all work with `--` (e.g. `--prune`); show the `--` form in help.
- Exactly one optional positional operand: the path to scan. Everything else is a flag.
- Running with no flags must be safe and read-only.

## Destructive actions
- Anything that deletes (`--prune`) is opt-in via an explicit flag, never the default.
- `--prune --dry-run` and the default report both show what would be removed without removing it.
- Skip dirty/locked worktrees unless `--force` is given; never remove abandoned worktrees automatically.
- No interactive confirmation prompt — the explicit `--prune` flag plus dry-run is the safeguard,
  and it keeps the tool scriptable.

## Machine output
- `--json` emits structured output for scripts. Keep the JSON schema stable and additive.

## Terminal-aware behaviour (colour, progress, paging)
- Detect a terminal with `isTTY`. All of the below degrade to plain, unpaged,
  uncoloured output when stdout/stderr is not a terminal, so pipes stay clean.
- **Colour**: the STATUS column is coloured only when stdout is a TTY and `NO_COLOR` is unset.
  Colour goes only in the last table column so it never disturbs `tabwriter` alignment.
- **Progress**: a transient stderr line, shown only on an interactive stderr and not under `--verbose`/`--quiet`.
- **`--verbose`** logs durable per-worktree detail to stderr; **`--quiet`** silences progress.
- **Paging**: human output is paged through `$PAGER` (default `less -FIRX`) on an
  interactive stdout, unless `--no-pager`. JSON is never paged.

## Robustness
- Every git subprocess runs under a timeout (`gitTimeout`) so a wedged repo
  becomes an error row instead of hanging the whole scan.

## Keep it boring
- The standard-library `flag` package is sufficient; do not add a CLI framework.
- Stay dependency-free (the Nix build relies on `vendorHash = null`).
- Flag and output changes must be additive — do not break existing invocations or the JSON schema.
- `just check` (gofmt + vet + test) must pass before committing.
