package main

import (
	"errors"
	"os/exec"
	"sort"
	"strings"
)

// git runs `git -C dir <args...>` and returns trimmed stdout.
func git(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
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
	full := append([]string{"-C", dir}, args...)
	return exec.Command("git", full...).Run() == nil
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

// resolveMainRef determines the repo's integration branch. When override is
// set it is used verbatim (after a resolvability check); otherwise detection
// tries, in order: a local main/master, each remote's HEAD, then each
// remote's main/master. It returns a display name and a resolvable git ref.
func resolveMainRef(repo, override string) (name, ref string, err error) {
	if override != "" {
		if gitOK(repo, "rev-parse", "--verify", "--quiet", override+"^{commit}") {
			return override, override, nil
		}
		return "", "", errors.New("main ref " + override + " not found in repo")
	}

	for _, b := range []string{"main", "master"} {
		if gitOK(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b) {
			return b, "refs/heads/" + b, nil
		}
	}

	remotes := orderedRemotes(repo)
	for _, rm := range remotes {
		head, e := git(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+rm+"/HEAD")
		if e == nil && gitOK(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+head) {
			return head, "refs/remotes/" + head, nil
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
	return "", "", errors.New("could not determine main branch (pass -main)")
}

// isMerged reports whether commit is an ancestor of mainRef (i.e. already
// integrated). It distinguishes a clean "not merged" from a real error.
func isMerged(repo, commit, mainRef string) (bool, error) {
	cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", commit, mainRef)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// removeWorktree detaches a worktree via `git worktree remove`.
func removeWorktree(repo, worktree string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktree)
	_, err := git(repo, args...)
	return err
}
