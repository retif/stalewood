package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Worktree is one Claude Code worktree dir plus its analysis against the
// owning repo's main branch.
type Worktree struct {
	Path      string `json:"path"`       // absolute path to the worktree dir
	Repo      string `json:"repo"`       // absolute path to the owning repo root
	Name      string `json:"name"`       // basename of the worktree dir
	Branch    string `json:"branch"`     // checked-out branch ("" when detached)
	Head      string `json:"head"`       // short HEAD sha
	MainRef   string `json:"main_ref"`   // main branch the merge check ran against
	Merged    bool   `json:"merged"`     // HEAD is an ancestor of MainRef
	Dirty     bool   `json:"dirty"`      // has uncommitted changes
	Detached  bool   `json:"detached"`   // HEAD is detached (no branch)
	SizeBytes int64  `json:"size_bytes"` // disk usage, -1 when not measured
	Err       string `json:"error,omitempty"`
}

// Status is a short human-readable classification for table output.
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

// Prunable reports whether the worktree's branch is fully merged and so the
// worktree is safe to remove. A "*" (dirty) worktree is prunable only with force.
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

// analyze inspects a single worktree directory and classifies it against its
// repo's main branch. withSize controls whether disk usage is measured.
func analyze(path string, withSize bool) Worktree {
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

	name, ref, err := resolveMainRef(w.Repo)
	if err != nil {
		w.Err = err.Error()
		return w
	}
	w.MainRef = name

	merged, err := isMerged(w.Repo, head, ref)
	if err != nil {
		w.Err = err.Error()
		return w
	}
	w.Merged = merged
	return w
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
