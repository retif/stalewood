# stalewood

Scans a directory tree for git worktrees and tells you which ones are safe to
delete — i.e. whose work is already integrated into another branch. Optionally
reaps them.

## Build & run

```sh
cd stalewood
go build -o stalewood .
./stalewood --size ~/projects
```

`go build` produces a self-contained binary; copy it onto your `PATH` (e.g.
`~/go/bin/`) to run it from anywhere. Or skip the build: `go run . ~/projects`.

With Nix: `nix develop` for the dev shell, `nix build` / `nix run` for the
tool. Common tasks are in the `justfile` — `just build`, `just test`,
`just check`, `just run --size ~/projects`.

## Usage

```sh
stalewood [flags] [path]
```

`path` defaults to the current directory.

| Flag         | Effect                                                            |
|--------------|-------------------------------------------------------------------|
| `--size`     | measure each worktree's disk usage                                |
| `--base REF` | test every worktree against `REF` instead of its own base         |
| `--lint SEL` | lint mode: exit 1 if a worktree matches `SEL` (repeatable)         |
| `--json`     | emit JSON instead of the tree                                     |
| `--prune`    | remove worktrees whose work is merged                             |
| `--force`    | with `--prune`, also remove merged worktrees that are dirty/locked |
| `--dry-run`  | with `--prune`, show what would be removed without removing it    |
| `--verbose`  | log per-worktree detail to stderr                                 |
| `--quiet`    | suppress progress output                                          |
| `--print`    | print the whole report at once (disable the pager)               |
| `--no-pager` | alias for `--print`                                               |
| `--version`  | print version and exit                                            |
| `--json-schema` | print the JSON Schema for `--json` output                  |
| `-h, --help` | show help                                                         |

Exit codes: `0` success, `1` runtime failure, `2` usage error.

### Examples

```sh
stalewood --size ~/projects             # report, with disk usage
stalewood --base oleks/main ~/repo      # force a specific base
stalewood --prune --dry-run ~/projects  # preview what --prune would remove
stalewood --prune ~/projects            # remove merged worktrees
stalewood --json ~/projects             # machine-readable output (grouped by repo)
```

## The report

The report is a tree grouped by repo. Each `●` node is a repo (with its full
path); each `├─`/`└─` node is a worktree showing a glyph, name, verdict and
tags; the `├──` leaves give the worktree's full path, branch and base.

```
● gitea   /home/oleks/projects/gitea
  ├─ ✗ gitea-toasts  unmerged [untracked]
  │  ├── path    /home/oleks/projects/gitea-toasts
  │  ├── branch  sse-toasts
  │  └── base    oleks/main
  └─ ✓ gitea-issue-fixes  merged -> oleks/main
     ├── path    /home/oleks/projects/gitea/.claude/worktrees/gitea-issue-fixes
     ├── branch  fix/issue-19-sse-state
     └── base    fix/user-project-move-multiproject-detach (sha)
```

A summary and a legend follow; the legend describes only the glyphs and tags
that actually appear in that run.

| Marker          | Meaning                                                       |
|-----------------|---------------------------------------------------------------|
| `✓` / `✗`       | merged / unmerged                                             |
| `⚠`             | abandoned (orphan dir or stale git entry)                     |
| `!`             | error — the worktree could not be analyzed                    |
| `-> REF`        | merged, but into `REF` — a branch other than its own base     |
| `[claude]`      | created by Claude Code (under `.claude/worktrees/`)           |
| `[modified files]`    | tracked files have uncommitted changes                        |
| `[untracked files]`   | the worktree has untracked files                              |
| `[locked]`      | a git worktree lock is held                                   |
| `[lock-stale]`  | locked, but the process that took the lock is gone            |
| `[git-prunable]`| git's own `worktree list` flags the entry prunable            |

## Discovery

Worktrees are found from three sources, unioned and de-duplicated by path:

1. **`.claude/worktrees/*`** — Claude Code worktree directories, found by
   walking the tree. `node_modules` and `.git` are skipped; a child with no
   `.git` entry (e.g. a committed test fixture) is not a worktree and is
   ignored.
2. **`git worktree list`** — every git repo found under the path is asked for
   its linked worktrees, so worktrees living *outside* `.claude/worktrees/`
   (e.g. ones you made by hand) are included too. The main checkout is not.
3. **Abandoned worktrees** — found by cross-referencing the two:
   - **orphan dir** (`abandoned-orphan`) — a worktree directory on disk that
     no repo's `git worktree list` knows about (its `.git` file points to a
     deleted git dir);
   - **stale entry** (`abandoned-stale`) — a `git worktree list` entry whose
     directory is gone.

   Abandoned worktrees carry no merge analysis; they show a `fix` leaf with
   the suggested cleanup.

## Merge classification

A live worktree counts as **merged** if either:

- its HEAD is an ancestor of its **base** branch
  (`git merge-base --is-ancestor`); or
- its HEAD is contained in **any branch other than its own**
  (`git for-each-ref --contains`) — catches work integrated into a branch
  other than the base.

### Base detection

By default each worktree is tested against the branch it was forked from. The
base is recovered in this order; the `base` leaf suffix shows which step won:

| Source        | Suffix       | How                                              |
|---------------|--------------|--------------------------------------------------|
| `--base REF`  | `(flag)`     | explicit override, applied to every worktree     |
| reflog ref    | *(none)*     | the branch's `Created from <ref>` reflog entry   |
| reflog SHA    | `(sha)`      | that reflog entry's commit, named via `name-rev` |
| upstream      | `(upstream)` | the branch's configured upstream branch          |
| auto          | `(auto)`     | the repo's main branch (remote `HEAD` preferred) |

The reflog-SHA step recovers a base even when the reflog ref is the unhelpful
literal `HEAD` or names a since-removed remote.

### Caveats

- **Squash / rebase merges.** Both checks are reachability-based; a branch that
  was squash-merged or rebased onto its target shows as `unmerged`. Verify by
  hand before force-pruning.
- **Sibling worktrees.** If two worktrees share commits, each may report the
  other's branch as containing its work. Check `merged -> REF` before pruning.

## Pruning

`--prune` runs `git worktree remove` on every **merged** worktree — anywhere,
not just under `.claude/worktrees/`. Running with `--prune --dry-run` (or
with no flags at all) reports exactly what `--prune` would remove without
touching anything. Unmerged worktrees are kept; a merged worktree that is
dirty or locked is skipped unless `--force` is given — a `[lock-stale]` skip
says so, since forcing it is safe. **Abandoned worktrees are never removed by
`--prune`.** Exit status is non-zero if any removal failed.

## Lint mode

`--lint` turns stalewood into a checker for a single repo — built for git
hooks. It scans only the git repo containing `[path]` (no directory walk, so it
is fast enough for `pre-push`) and **exits 1 if any worktree matches**.

Each `--lint` value is a comma-separated **AND-group** of predicates; repeat
`--lint` to **OR** the groups; prefix a predicate with `!` to negate it.

```sh
stalewood --lint abandoned                    # fail if any abandoned worktree
stalewood --lint abandoned --lint lock-stale  # abandoned OR lock-stale
stalewood --lint removable,manual             # merged AND not a Claude worktree
stalewood --lint merged,untracked             # merged AND has untracked files
```

Predicates: `merged` `unmerged` `live` `abandoned` `orphan` `stale` `dirty`
`modified` `untracked` `locked` `lock-stale` `claude` `manual` `detached`
`error` `git-prunable` `removable` `any`.

Matching worktrees are printed; exit status is `1` on a match, `0` when clean
(and silent), `2` on a bad selector. Use it in a global `pre-push` hook
(`git config --global core.hooksPath <dir>`):

```sh
#!/bin/sh
# <hooks>/pre-push - block pushes while stale worktrees linger
exec stalewood --lint abandoned --lint lock-stale
```

## Terminal behaviour

stalewood adapts to where its output goes:

- **Colour & weight** — glyphs and verdicts are bold and colour-coded by
  severity, repo nodes bold-cyan, connectors dim. On an interactive terminal
  only; disabled when piped or when `NO_COLOR` is set.
- **Progress** — a transient progress line is shown on an interactive stderr
  during a scan. `--quiet` silences it; `--verbose` replaces it with durable
  per-worktree log lines on stderr.
- **Paging** — human output is paged through `$PAGER` (default `less -FIRX`)
  on an interactive terminal; `--no-pager` disables it. JSON is never paged.

Piped or redirected, output is plain, unpaged and uncoloured. Every git
subprocess runs under a timeout so a wedged repo cannot stall the scan.

## Contributing

CLI behaviour follows [clig.dev](https://clig.dev) where reasonable — see
`CLAUDE.md` for the rules. Run `just check` (gofmt + vet + test) before
committing.
