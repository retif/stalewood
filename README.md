# stale-worktrees

Finds Claude Code worktree directories (`.claude/worktrees/*`) under a path and
tells you which ones are safe to delete — i.e. whose work is already
integrated into another branch. Optionally reaps them.

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
| `-prune`    | remove worktrees whose work is merged                             |
| `-force`    | with `-prune`, also remove merged worktrees with uncommitted edits |

### Examples

```sh
stale-worktrees -size ~/projects             # report, with disk usage
stale-worktrees -base oleks/main ~/projects/gitea   # force a specific base
stale-worktrees -prune ~/projects             # delete the merged worktrees
stale-worktrees -json ~/projects              # machine-readable output
```

## How it works

1. **Detect** — walks the tree for any `.claude/worktrees` directory and takes
   each immediate child *that carries its own `.git` entry* as a worktree.
   `node_modules` and `.git` are skipped, and a worktrees dir is not descended
   into, so nested fixtures are not double-counted. Detection is by path
   convention, not `git worktree list` — so an abandoned worktree git has
   forgotten is still found. A directory with no `.git` entry (e.g. a committed
   test fixture that merely sits under such a path) is not a worktree and is
   skipped silently; a child that has a `.git` entry but is not a valid linked
   worktree is surfaced as an error row.
2. **Classify** — a worktree counts as **merged** if either:
   - its HEAD is an ancestor of its **base** branch
     (`git merge-base --is-ancestor`); or
   - its HEAD is contained in **any branch other than its own**
     (`git for-each-ref --contains`) — this catches work that landed on a
     branch other than the base.

   A `*` marks uncommitted changes. `merged -> REF` means the work was found in
   `REF`, a branch other than the worktree's own base.

### Base detection

By default each worktree is tested against **the branch it was forked from**.
The base is recovered in this order, and the `BASE` column suffix shows which
step succeeded:

| Source        | Suffix       | How                                              |
|---------------|--------------|--------------------------------------------------|
| `-base REF`   | `(flag)`     | explicit override, applied to every worktree     |
| reflog ref    | *(none)*     | the branch's `Created from <ref>` reflog entry   |
| reflog SHA    | `(sha)`      | that reflog entry's commit, named via `name-rev` |
| upstream      | `(upstream)` | the branch's configured upstream branch          |
| auto          | `(auto)`     | the repo's main branch (remote `HEAD` preferred) |

The reflog-SHA step recovers a base even when the reflog ref is the unhelpful
literal `HEAD`, or names a remote that has since been removed — the commit SHA
in the reflog entry is stable where the ref name is not.

When no base can be recovered at all, the base is left blank (`-`) and the
verdict comes from the contains check alone.

### Caveats

- **Squash / rebase merges.** Both checks are reachability-based. A branch that
  was squash-merged or rebased onto its target shows as `unmerged`, because its
  original commits are not present there. Verify by hand before force-pruning.
- **Sibling worktrees.** If two worktrees share commits, each may report the
  other's branch as containing its work. Check `merged -> REF` before pruning.

## Pruning

`-prune` runs `git worktree remove` on every **merged** worktree. Unmerged
worktrees are always kept. A merged worktree with uncommitted changes is
skipped unless `-force` is given. Exit status is non-zero if any removal failed.
