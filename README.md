# stale-worktrees

Finds Claude Code worktree directories (`.claude/worktrees/*`) under a path and
tells you which ones are safe to delete — i.e. whose branch is already merged
into the branch it was forked from. Optionally reaps the merged ones.

## Build & run

```sh
cd stale-worktrees
go build -o stale-worktrees .
./stale-worktrees -size ~/projects
```

`go build` produces a self-contained binary; copy it onto your `PATH` (e.g.
`~/go/bin/`) to run it from anywhere. Or skip the build: `go run . ~/projects`.

## Usage

```sh
stale-worktrees [flags] [path]
```

`path` defaults to the current directory.

| Flag        | Effect                                                            |
|-------------|-------------------------------------------------------------------|
| `-size`     | measure each worktree's disk usage                                |
| `-base REF` | test every worktree against `REF` instead of its own base         |
| `-json`     | emit JSON instead of a table                                      |
| `-prune`    | remove worktrees whose branch is merged into its base             |
| `-force`    | with `-prune`, also remove merged worktrees with uncommitted edits |

### Examples

```sh
stale-worktrees -size ~/projects             # report, with disk usage
stale-worktrees -base oleks/main ~/projects/gitea   # force a specific base
stale-worktrees -prune ~/projects             # delete the merged worktrees
stale-worktrees -json ~/projects              # machine-readable output
```

## How it works

1. **Detect** — walks the tree for any `.claude/worktrees` directory and
   treats each immediate child as a worktree. `node_modules` and `.git` are
   skipped; a worktrees dir is not descended into, so nested fixtures are not
   double-counted. A child that is not actually a git worktree is reported as
   an error rather than silently dropped.
2. **Classify** — for each worktree it reads the checked-out branch and HEAD,
   resolves the **base** (below), and runs
   `git merge-base --is-ancestor HEAD <base>`. If HEAD is an ancestor, the
   branch is **merged**; otherwise **unmerged**. A `*` marks uncommitted changes.

### Base detection

By default each worktree is tested against **the branch it was forked from**,
not a fixed `main`. The base is resolved in this order:

1. **`-base REF`** — if given, every worktree is tested against `REF`.
2. **Reflog** — the ref recorded in the branch's `Created from` reflog entry
   (`git worktree add` / `git branch` writes this at creation).
3. **Auto** — if the reflog is gone or says `Created from HEAD`, the repo's
   `main`/`master` is used (walking remotes, `origin`/`upstream` first).

The `BASE` column shows the ref used. `(auto)` means the base could not be
recovered and a main branch was substituted; `(flag)` means `-base` was given.
No suffix means the base came straight from the reflog.

Testing against the true base catches work that landed on a branch other than
`main` — e.g. a commit pushed to `origin/main` while the local `main` lagged
behind would be missed by a fixed-`main` check but is correctly seen as merged.

### Caveat: squash and rebase merges

The merge check is ancestry-based. A branch that was **squash-merged** or
**rebased** onto its base will show as `unmerged`, because its original commits
are not ancestors of the base tip. Treat `unmerged` as "not *fast-forward*
merged"; verify by hand before force-pruning such branches.

## Pruning

`-prune` runs `git worktree remove` on every **merged** worktree. Unmerged
worktrees, and any whose base could not be determined, are always kept.
A merged worktree with uncommitted changes is skipped unless `-force` is given.
Exit status is non-zero if any removal failed.
