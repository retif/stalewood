package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Worktree is one Claude Code worktree dir plus its analysis against the
// branch it was forked from.
type Worktree struct {
	Path      string `json:"path"`       // absolute path to the worktree dir
	Repo      string `json:"repo"`       // absolute path to the owning repo root
	Name      string `json:"name"`       // basename of the worktree dir
	Branch    string `json:"branch"`     // checked-out branch ("" when detached)
	Head      string `json:"head"`       // short HEAD sha
	Base      string `json:"base"`       // ref the merge check ran against
	BaseFrom  string `json:"base_from"`  // how Base was chosen: reflog | auto | flag
	Merged    bool   `json:"merged"`     // HEAD is an ancestor of Base
	Dirty     bool   `json:"dirty"`      // has uncommitted changes
	Detached  bool   `json:"detached"`   // HEAD is detached (no branch)
	SizeBytes int64  `json:"size_bytes"` // disk usage, -1 when not measured
	Err       string `json:"error,omitempty"`
}

// Status is a short human-readable classification for table output.
// A trailing "*" marks a worktree with uncommitted changes.
func (w *Worktree) Status() string {
	switch {
	case w.Err != "":
		return "error"
	case w.Merged && w.Dirty:
		return "merged*"
	case w.Merged:
		return "merged"
	case w.Dirty:
		return "unmerged*"
	default:
		return "unmerged"
	}
}

// Prunable reports whether the worktree's branch is fully merged into its
// base and so the worktree is safe to remove. A dirty merged worktree is
// prunable only with force.
func (w *Worktree) Prunable() bool {
	return w.Err == "" && w.Merged
}

// skipDirs are never descended into during the scan.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
}

// findWorktreesDirs walks root and returns every ".claude/worktrees" directory.
// It does not descend into a worktrees dir once found, so nested test fixtures
// or worktrees-within-worktrees are not double-counted.
func findWorktreesDirs(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, keep walking
		}
		if !d.IsDir() {
			return nil
		}
		if skipDirs[d.Name()] {
			return fs.SkipDir
		}
		if d.Name() == "worktrees" && filepath.Base(filepath.Dir(p)) == ".claude" {
			found = append(found, p)
			return fs.SkipDir
		}
		return nil
	})
	sort.Strings(found)
	return found, err
}

// collectWorktrees finds every worktrees dir under root and returns the
// immediate child directories — the individual worktrees themselves.
func collectWorktrees(root string) ([]string, error) {
	dirs, err := findWorktreesDirs(root)
	if err != nil {
		return nil, err
	}
	var wts []string
	for _, d := range dirs {
		entries, e := os.ReadDir(d)
		if e != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				wts = append(wts, filepath.Join(d, ent.Name()))
			}
		}
	}
	sort.Strings(wts)
	return wts, nil
}

// analyze inspects a single worktree directory and classifies it against the
// branch it was forked from. withSize controls whether disk usage is measured.
// baseOverride, when non-empty, forces the ref the merge check runs against;
// otherwise the base is recovered per-worktree from the reflog, falling back
// to the repo's auto-detected main branch.
func analyze(path string, withSize bool, baseOverride string) Worktree {
	w := Worktree{Path: path, Name: filepath.Base(path), SizeBytes: -1}
	// repo root: <repo>/.claude/worktrees/<name> -> up three levels.
	w.Repo = filepath.Dir(filepath.Dir(filepath.Dir(path)))

	if withSize {
		if sz, err := dirSize(path); err == nil {
			w.SizeBytes = sz
		}
	}

	if !isLinkedWorktree(path) {
		w.Err = "not a git worktree"
		return w
	}

	head, err := headOf(path)
	if err != nil {
		w.Err = err.Error()
		return w
	}
	w.Head = short(head)
	w.Branch = branchOf(path)
	w.Detached = w.Branch == ""
	w.Dirty = isDirty(path)

	name, ref, from, err := resolveBase(w.Repo, w.Branch, baseOverride)
	if err != nil {
		w.Err = err.Error()
		return w
	}
	w.Base = name
	w.BaseFrom = from

	merged, err := isMerged(w.Repo, head, ref)
	if err != nil {
		w.Err = err.Error()
		return w
	}
	w.Merged = merged
	return w
}

// resolveBase picks the ref to test a worktree branch against. It returns a
// display name, a resolvable git ref, and how the choice was made:
//
//	flag   - the explicit -base override
//	reflog - recovered from the branch's "Created from" reflog entry
//	auto   - fell back to the repo's main/master branch
//
// Precedence is override > reflog > auto.
func resolveBase(repo, branch, override string) (name, ref, from string, err error) {
	if override != "" {
		n, r, e := autoMainRef(repo, override)
		return n, r, "flag", e
	}
	if b, ok := detectBase(repo, branch); ok {
		return b, b, "reflog", nil
	}
	n, r, e := autoMainRef(repo, "")
	return n, r, "auto", e
}

// dirSize sums the apparent size of all regular files under path.
func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
