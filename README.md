# stale-worktrees

Finds Claude Code worktree directories (`.claude/worktrees/*`) under a path and
tells you which ones are safe to delete — i.e. whose branch is already merged
into the owning repo's main branch. Optionally reaps the merged ones.

## Build

```sh
go build -o stale-worktrees .
```

## Usage

```sh
stale-worktrees [flags] [path]
```

`path` defaults to the current directory.

| Flag        | Effect                                                            |
|-------------|-------------------------------------------------------------------|
| `-size`     | measure each worktree's disk usage                                |
| `-main REF` | branch/ref to test merge against (default: auto-detect)           |
| `-json`     | emit JSON instead of a table                                      |
| `-prune`    | remove worktrees whose branch is merged into main                 |
| `-force`    | with `-prune`, also remove merged worktrees with uncommitted edits |

### Examples

```sh
stale-worktrees -size ~/projects            # report, with disk usage
stale-worktrees -main oleks/main ~/projects/gitea   # check against a fork branch
stale-worktrees -prune ~/projects            # delete the merged worktrees
stale-worktrees -json ~/projects             # machine-readable output
```

## How it works

1. **Detect** — walks the tree for any `.claude/worktrees` directory and
   treats each immediate child as a worktree. `node_modules` and `.git` are
   skipped; a worktrees dir is not descended into, so nested fixtures are not
   double-counted.
2. **Classify** — for each worktree it reads the checked-out branch and HEAD,
   resolves the owning repo's main branch, and runs
   `git merge-base --is-ancestor HEAD <main>`. If HEAD is an ancestor, the
   branch is **merged**; otherwise **unmerged**. A `*` marks uncommitted changes.

### Main-branch detection

With no `-main` flag the integration branch is auto-detected, in order:

1. a local `main` or `master`;
2. each remote's `HEAD` (`origin` and `upstream` tried first);
3. each remote's `main` / `master`.

Fork checkouts without a local `main` therefore resolve against
`upstream/main` (or whatever the remote HEAD points at). Pass `-main` to force
a specific ref — useful when "merged" should mean merged into *your* fork
rather than upstream.

## Pruning

`-prune` runs `git worktree remove` on every **merged** worktree. Unmerged
worktrees and any whose main branch could not be determined are always kept.
A merged worktree with uncommitted changes is skipped unless `-force` is given.
Exit status is non-zero if any removal failed.
