# stalewood

Scans a directory tree for git worktrees and tells you which ones are safe to
delete — i.e. whose work is already integrated into another branch. Optionally
reaps them.

## Build & run

```sh
cd stalewood
go build -o stalewood .
./stalewood -size ~/projects
```

`go build` produces a self-contained binary; copy it onto your `PATH` (e.g.
`~/go/bin/`) to run it from anywhere. Or skip the build: `go run . ~/projects`.

## Usage

```sh
stalewood [flags] [path]
```

`path` defaults to the current directory.

| Flag        | Effect                                                            |
|-------------|-------------------------------------------------------------------|
| `-size`     | measure each worktree's disk usage                                |
| `-base REF` | test every worktree against `REF` instead of its own base         |
| `-json`     | emit JSON instead of a table                                      |
| `-prune`    | remove worktrees whose work is merged                             |
| `-force`    | with `-prune`, also remove merged worktrees that are dirty/locked  |

### Examples

```sh
stalewood -size ~/projects             # report, with disk usage
stalewood -base oleks/main ~/repo      # force a specific base
stalewood -prune ~/projects            # remove merged worktrees
stalewood -json ~/projects             # machine-readable output
```

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

   Abandoned worktrees carry no merge analysis; they are reported with a
   suggested fix (`git worktree prune` for stale entries; manual removal for
   orphan dirs).

## Merge classification

A live worktree counts as **merged** if either:

- its HEAD is an ancestor of its **base** branch
  (`git merge-base --is-ancestor`); or
- its HEAD is contained in **any branch other than its own**
  (`git for-each-ref --contains`) — catches work integrated into a branch
  other than the base.

`*` marks uncommitted changes, `[locked]` marks a locked worktree, and
`merged -> REF` means the work was found in `REF`, a branch other than the
worktree's own base.

### Base detection

By default each worktree is tested against the branch it was forked from. The
base is recovered in this order; the `BASE` column suffix shows which step won:

| Source        | Suffix       | How                                              |
|---------------|--------------|--------------------------------------------------|
| `-base REF`   | `(flag)`     | explicit override, applied to every worktree     |
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

`-prune` runs `git worktree remove` on every **merged** worktree — anywhere,
not just under `.claude/worktrees/`. Unmerged worktrees are kept. A merged
worktree that is dirty or locked is skipped unless `-force` is given.
**Abandoned worktrees are never removed by `-prune`** — they are reported with
the suggested fix and left for you. Exit status is non-zero if any removal failed.
