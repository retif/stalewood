package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// gitTimeout bounds every git subprocess so a hung or wedged repo cannot stall
// the whole scan; the call surfaces as an error row instead.
const gitTimeout = 60 * time.Second

// git runs `git -C dir <args...>` and returns trimmed stdout.
func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), gitTimeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", &gitError{args: args, stderr: strings.TrimSpace(string(ee.Stderr)), code: ee.ExitCode()}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitOK runs a git command and reports only whether it exited 0.
func gitOK(dir string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	return exec.CommandContext(ctx, "git", full...).Run() == nil
}

type gitError struct {
	args   []string
	stderr string
	code   int
}

func (e *gitError) Error() string {
	msg := "git " + strings.Join(e.args, " ")
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}

// isLinkedWorktree reports whether dir is a linked git worktree (created by
// `git worktree add`), as opposed to a primary checkout or a non-repo dir.
// Linked worktrees keep their git dir under <repo>/.git/worktrees/<name>.
func isLinkedWorktree(dir string) bool {
	gd, err := git(dir, "rev-parse", "--git-dir")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ReplaceAll(gd, "\\", "/"), "/worktrees/")
}

// commonGitDir returns the absolute shared git directory of the working tree
// at dir (the repo's .git). ok is false when dir is not a usable working tree
// — e.g. an orphan worktree whose backing git dir has been deleted.
func commonGitDir(dir string) (string, bool) {
	if out, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && out != "" {
		return filepath.Clean(out), true
	}
	out, err := git(dir, "rev-parse", "--git-common-dir")
	if err != nil || out == "" {
		return "", false
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	return filepath.Clean(out), true
}

// wtEntry is one linked worktree from `git worktree list --porcelain`.
type wtEntry struct {
	Path     string
	Head     string
	Branch   string // short name, "" when detached
	Detached bool
	Bare     bool
	Locked   bool
	Prunable bool
}

// listWorktrees parses `git worktree list --porcelain` for the repo containing
// dir. The first record (the main working tree, or a bare repo) is dropped, so
// only linked worktrees are returned.
func listWorktrees(dir string) ([]wtEntry, error) {
	out, err := git(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var all []wtEntry
	var cur *wtEntry
	flush := func() {
		if cur != nil {
			all = append(all, *cur)
			cur = nil
		}
	}
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			flush()
			cur = &wtEntry{Path: strings.TrimSpace(strings.TrimPrefix(ln, "worktree "))}
		case cur == nil:
			// between records
		case ln == "bare":
			cur.Bare = true
		case ln == "detached":
			cur.Detached = true
		case strings.HasPrefix(ln, "HEAD "):
			cur.Head = strings.TrimSpace(strings.TrimPrefix(ln, "HEAD "))
		case strings.HasPrefix(ln, "branch "):
			cur.Branch = shortRef(strings.TrimSpace(strings.TrimPrefix(ln, "branch ")))
		case strings.HasPrefix(ln, "locked"):
			cur.Locked = true
		case strings.HasPrefix(ln, "prunable"):
			cur.Prunable = true
		}
	}
	flush()
	if len(all) > 0 {
		all = all[1:] // drop the main working tree / bare repo
	}
	return all, nil
}

// branchOf returns the checked-out branch of a worktree, or "" if detached.
func branchOf(dir string) string {
	b, err := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return b
}

// headOf returns the HEAD commit SHA of a worktree.
func headOf(dir string) (string, error) {
	return git(dir, "rev-parse", "HEAD")
}

// isDirty reports whether the worktree has uncommitted changes.
func isDirty(dir string) bool {
	out, err := git(dir, "status", "--porcelain")
	return err == nil && out != ""
}

// leaf returns the last "/"-separated segment of a ref name.
func leaf(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// shortRef strips the refs/heads/ or refs/remotes/ prefix from a full ref.
func shortRef(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/remotes/")
	return ref
}

// baseHint is a recovered fork base: a display name, a resolvable git ref,
// and the method that found it (reflog | reflog-sha | upstream).
type baseHint struct {
	name, ref, from string
}

// reflogCreation returns the ref name and commit SHA recorded in a branch's
// "Created from" reflog entry (the branch's creation event), or empty strings.
func reflogCreation(repo, branch string) (ref, sha string) {
	out, err := git(repo, "reflog", "show", branch)
	if err != nil || out == "" {
		return "", ""
	}
	const marker = ": branch: Created from "
	for _, ln := range strings.Split(out, "\n") {
		if i := strings.Index(ln, marker); i >= 0 {
			ref = strings.TrimSpace(ln[i+len(marker):])
			if f := strings.Fields(ln); len(f) > 0 {
				sha = f[0]
			}
		}
	}
	return ref, sha
}

// nameRevBranch names a commit via `git name-rev`, restricted to branches, and
// returns the branch it sits on with any ~N/^N suffix stripped. It returns
// ok=false when the commit cannot be tied to a branch other than self.
func nameRevBranch(repo, sha, selfBranch string) (string, bool) {
	out, err := git(repo, "name-rev", "--name-only",
		"--refs=refs/heads/*", "--refs=refs/remotes/*", sha)
	if err != nil || out == "" || strings.Contains(out, "undefined") {
		return "", false
	}
	name := out
	if i := strings.IndexAny(name, "~^"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimPrefix(name, "remotes/")
	if name == "" || name == selfBranch || strings.HasPrefix(name, "tags/") {
		return "", false
	}
	if !gitOK(repo, "rev-parse", "--verify", "--quiet", name+"^{commit}") {
		return "", false
	}
	return name, true
}

// upstreamOf returns a branch's configured upstream (e.g. "origin/main"), or "".
func upstreamOf(repo, branch string) string {
	out, err := git(repo, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// isSelfRemote reports whether upstream is just the branch's own pushed copy
// (<remote>/<branch>) rather than a distinct base branch.
func isSelfRemote(repo, branch, upstream string) bool {
	for _, r := range orderedRemotes(repo) {
		if upstream == r+"/"+branch {
			return true
		}
	}
	return false
}

// detectBase recovers the ref a worktree branch was forked from. It tries, in
// order: the reflog "Created from" ref (when it still names a branch); that
// reflog entry's SHA, named via name-rev (handles "Created from HEAD" and
// removed-remote refs); and finally the branch's upstream when it is a
// distinct branch. It returns ok=false when nothing usable is found.
func detectBase(repo, branch string) (baseHint, bool) {
	if branch == "" {
		return baseHint{}, false
	}
	createRef, createSHA := reflogCreation(repo, branch)

	if createRef != "" && createRef != "HEAD" {
		if full, err := git(repo, "rev-parse", "--symbolic-full-name", createRef); err == nil {
			if strings.HasPrefix(full, "refs/heads/") || strings.HasPrefix(full, "refs/remotes/") {
				return baseHint{shortRef(createRef), createRef, "reflog"}, true
			}
		}
	}
	if createSHA != "" {
		if n, ok := nameRevBranch(repo, createSHA, branch); ok {
			return baseHint{n, n, "reflog-sha"}, true
		}
	}
	if up := upstreamOf(repo, branch); up != "" && !isSelfRemote(repo, branch, up) {
		if gitOK(repo, "rev-parse", "--verify", "--quiet", up+"^{commit}") {
			return baseHint{up, up, "upstream"}, true
		}
	}
	return baseHint{}, false
}

// containedElsewhere reports whether a worktree's HEAD is reachable from any
// branch other than the worktree's own branch (or its pushed remote copy).
// This catches work that was integrated into a branch other than its base.
func containedElsewhere(repo, branch, head string) (string, bool) {
	out, err := git(repo, "for-each-ref", "--contains", head,
		"--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil || out == "" {
		return "", false
	}
	skip := map[string]bool{"refs/heads/" + branch: true}
	for _, r := range orderedRemotes(repo) {
		skip["refs/remotes/"+r+"/"+branch] = true
	}
	var cands []string
	for _, ref := range strings.Split(out, "\n") {
		ref = strings.TrimSpace(ref)
		if ref == "" || skip[ref] || strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		cands = append(cands, ref)
	}
	if len(cands) == 0 {
		return "", false
	}
	// Prefer a main/master branch for a stable, meaningful answer.
	for _, ref := range cands {
		if l := leaf(ref); l == "main" || l == "master" {
			return shortRef(ref), true
		}
	}
	return shortRef(cands[0]), true
}

// orderedRemotes lists the repo's remotes with origin and upstream first,
// the rest alphabetical — the order auto-detection tries them in.
func orderedRemotes(repo string) []string {
	out, err := git(repo, "remote")
	if err != nil || out == "" {
		return nil
	}
	all := strings.Fields(out)
	rank := func(r string) int {
		switch r {
		case "origin":
			return 0
		case "upstream":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if ri, rj := rank(all[i]), rank(all[j]); ri != rj {
			return ri < rj
		}
		return all[i] < all[j]
	})
	return all
}

// autoMainRef determines a repo's integration branch when no base could be
// recovered for a worktree. When override is set it is used verbatim (after a
// resolvability check). Otherwise detection prefers each remote's HEAD first —
// that is what Claude Code's worktree creation forks from — then a local
// main/master, then any remote main/master.
func autoMainRef(repo, override string) (name, ref string, err error) {
	if override != "" {
		if gitOK(repo, "rev-parse", "--verify", "--quiet", override+"^{commit}") {
			return override, override, nil
		}
		return "", "", errors.New("base ref " + override + " not found in repo")
	}

	remotes := orderedRemotes(repo)
	for _, rm := range remotes {
		head, e := git(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+rm+"/HEAD")
		if e == nil && gitOK(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+head) {
			return head, "refs/remotes/" + head, nil
		}
	}
	for _, b := range []string{"main", "master"} {
		if gitOK(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b) {
			return b, "refs/heads/" + b, nil
		}
	}
	for _, rm := range remotes {
		for _, b := range []string{"main", "master"} {
			rb := rm + "/" + b
			if gitOK(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+rb) {
				return rb, "refs/remotes/" + rb, nil
			}
		}
	}
	return "", "", errors.New("could not determine base branch (pass --base)")
}

// isMerged reports whether commit is an ancestor of baseRef (i.e. already
// integrated). It distinguishes a clean "not merged" from a real error.
func isMerged(repo, commit, baseRef string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	err := exec.CommandContext(ctx, "git", "-C", repo, "merge-base", "--is-ancestor", commit, baseRef).Run()
	if err == nil {
		return true, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return false, fmt.Errorf("merge-base --is-ancestor: timed out after %s", gitTimeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// removeWorktree detaches a worktree via `git worktree remove` (timed out via git()).
func removeWorktree(repo, worktree string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktree)
	_, err := git(repo, args...)
	return err
}
