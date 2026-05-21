package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Worktree is one Claude Code worktree dir plus its analysis: which branch it
// was forked from, and whether its work is already integrated elsewhere.
type Worktree struct {
	Path       string `json:"path"`                  // absolute path to the worktree dir
	Repo       string `json:"repo"`                  // absolute path to the owning repo root
	Name       string `json:"name"`                  // basename of the worktree dir
	Branch     string `json:"branch"`                // checked-out branch ("" when detached)
	Head       string `json:"head"`                  // short HEAD sha
	Base       string `json:"base"`                  // recovered fork base ("" when unknown)
	BaseFrom   string `json:"base_from"`             // how Base was chosen
	Merged     bool   `json:"merged"`                // work is integrated (see MergedInto)
	MergedInto string `json:"merged_into,omitempty"` // ref the work was found in
	Dirty      bool   `json:"dirty"`                 // has uncommitted changes
	Detached   bool   `json:"detached"`              // HEAD is detached (no branch)
	SizeBytes  int64  `json:"size_bytes"`            // disk usage, -1 when not measured
	Err        string `json:"error,omitempty"`
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

// Prunable reports whether the worktree's work is integrated and so the
// worktree is safe to remove. A dirty merged worktree is prunable only with force.
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

// hasGitEntry reports whether path has its own ".git" entry — a file for a
// linked worktree, a directory for a full checkout. A directory that merely
// sits under a .claude/worktrees path (e.g. a committed test fixture) has
// none, and is not a worktree.
func hasGitEntry(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

// collectWorktrees finds every worktrees dir under root and returns the
// immediate child directories that are worktrees — i.e. that carry their own
// ".git" entry. Plain directories that only happen to sit under a
// .claude/worktrees path are skipped.
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
			if !ent.IsDir() {
				continue
			}
			child := filepath.Join(d, ent.Name())
			if hasGitEntry(child) {
				wts = append(wts, child)
			}
		}
	}
	sort.Strings(wts)
	return wts, nil
}

// analyze inspects a single worktree directory and decides whether its work is
// integrated. withSize controls whether disk usage is measured. baseOverride,
// when non-empty, forces the ref the merge check runs against.
//
// A worktree counts as merged if its HEAD is an ancestor of its base, OR if
// HEAD is contained in some branch other than its own. The base is recovered
// per-worktree (reflog, then name-rev, then upstream); when it cannot be
// recovered the base is left blank but the contains check still gives a verdict.
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

	name, ref, from, baseErr := resolveBase(w.Repo, w.Branch, baseOverride)
	if baseErr != nil && baseOverride != "" {
		w.Err = baseErr.Error() // a user-supplied base must resolve
		return w
	}
	if baseErr == nil {
		w.Base, w.BaseFrom = name, from
		merged, e := isMerged(w.Repo, head, ref)
		if e != nil {
			w.Err = e.Error()
			return w
		}
		if merged {
			w.Merged, w.MergedInto = true, name
		}
	}

	// Not an ancestor of the base (or base unknown): is the work present in
	// any other branch? Catches integration into a non-base branch.
	if !w.Merged && !w.Detached {
		if cref, ok := containedElsewhere(w.Repo, w.Branch, head); ok {
			w.Merged, w.MergedInto = true, cref
		}
	}
	return w
}

// resolveBase picks the ref to test a worktree branch against. It returns a
// display name, a resolvable git ref, and how the choice was made:
//
//	flag       - the explicit -base override
//	reflog     - the branch's "Created from" reflog ref
//	reflog-sha - that reflog entry's SHA, named via name-rev
//	upstream   - the branch's configured upstream branch
//	auto       - fell back to the repo's main/master branch
//
// err is non-nil only when an explicit override fails to resolve, or when no
// base could be determined at all.
func resolveBase(repo, branch, override string) (name, ref, from string, err error) {
	if override != "" {
		n, r, e := autoMainRef(repo, override)
		return n, r, "flag", e
	}
	if h, ok := detectBase(repo, branch); ok {
		return h.name, h.ref, h.from, nil
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
