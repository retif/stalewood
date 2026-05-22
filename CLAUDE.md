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
- `-h` / `--help` prints help to **stdout** and exits `0`.
- A flag/argument error prints the message + usage to **stderr** and exits `2`.
- `--version` prints `stalewood <version>` to stdout and exits `0`.
- Keep `--help` concise: summary line, usage line, flags, exit codes, a few examples.

## Flags & arguments
- Flag names are full words and all work with `--` (e.g. `--prune`); show the `--` form in help.
- Exactly one optional positional operand: the path to scan. Everything else is a flag.
- Running with no flags must be safe and read-only.

## Destructive actions
- Anything that deletes (`--prune`) is opt-in via an explicit flag, never the default.
- The default report mode *is* the dry run — it shows exactly what `--prune` would remove.
- Skip dirty/locked worktrees unless `--force` is given; never remove abandoned worktrees automatically.
- No interactive confirmation prompt — the explicit `--prune` flag plus the
  report-as-dry-run is the safeguard, and it keeps the tool scriptable.

## Machine output
- `--json` emits structured output for scripts. Keep the JSON schema stable and additive.

## Colour / TTY
- No colour today. If colour is added, emit it only when stdout is a TTY and honour `NO_COLOR`.

## Keep it boring
- The standard-library `flag` package is sufficient; do not add a CLI framework.
- Flag and output changes must be additive — do not break existing invocations or the JSON schema.
- `just check` (gofmt + vet + test) must pass before committing.
