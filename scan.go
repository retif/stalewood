package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Worktree is one linked git worktree plus its analysis: how it was
// discovered, which branch it was forked from, and whether its work is
// already integrated elsewhere.
type Worktree struct {
	Path        string `json:"path"`                   // absolute path to the worktree dir
	Repo        string `json:"repo"`                   // absolute path to the owning repo root
	Name        string `json:"name"`                   // basename of the worktree dir
	Kind        string `json:"kind"`                   // live | abandoned-orphan | abandoned-stale
	Claude      bool   `json:"claude"`                 // lives under a .claude/worktrees/ path
	Registered  bool   `json:"registered"`             // listed by `git worktree list`
	OnDisk      bool   `json:"on_disk"`                // the directory exists
	Locked      bool   `json:"locked,omitempty"`       // git worktree lock is set
	LockReason  string `json:"lock_reason,omitempty"`  // reason recorded with the lock
	GitPrunable bool   `json:"git_prunable,omitempty"` // git's worktree list flags it prunable
	Branch      string `json:"branch"`                 // checked-out branch ("" when detached)
	Head        string `json:"head"`                   // short HEAD sha
	Base        string `json:"base"`                   // recovered fork base ("" when unknown)
	BaseFrom    string `json:"base_from,omitempty"`    // how Base was chosen
	Merged      bool   `json:"merged"`                 // work is integrated (see MergedInto)
	MergedInto  string `json:"merged_into,omitempty"`  // ref the work was found in
	Dirty       bool   `json:"dirty"`                  // has uncommitted changes
	Detached    bool   `json:"detached"`               // HEAD is detached (no branch)
	SizeBytes   int64  `json:"size_bytes"`             // disk usage, -1 when not measured
	Err         string `json:"error,omitempty"`
}

// Status is a short classification for a live worktree.
// A trailing "*" marks uncommitted changes.
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

// Prunable reports whether `git worktree remove` should run on this worktree:
// a live worktree whose work is merged. Abandoned worktrees are never prunable
// (they are report-only).
func (w *Worktree) Prunable() bool {
	return w.Err == "" && w.Kind == "live" && w.Merged
}

// LockStale reports whether the worktree is locked but the process that took
// the lock is gone — a stale lock left behind by a dead owner.
func (w Worktree) LockStale() bool {
	if !w.Locked {
		return false
	}
	pid, ok := lockOwnerPID(w.LockReason)
	return ok && !pidAlive(pid)
}

// kindOf classifies a worktree by how it was discovered.
func kindOf(w *Worktree) string {
	switch {
	case w.Registered && w.OnDisk:
		return "live"
	case w.Registered && !w.OnDisk:
		return "abandoned-stale" // git tracks it, the directory is gone
	default:
		return "abandoned-orphan" // on disk, git no longer tracks it
	}
}

// skipDirs are never descended into during the scan.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
}

// hasGitEntry reports whether path has its own ".git" entry — a file for a
// linked worktree, a directory for a full checkout. A directory that merely
// sits under a .claude/worktrees path (e.g. a committed test fixture) has
// none, and is not a worktree.
func hasGitEntry(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

// underClaudeWorktrees reports whether path is an immediate child of a
// ".claude/worktrees" directory.
func underClaudeWorktrees(path string) bool {
	d := filepath.Dir(path)
	return filepath.Base(d) == "worktrees" && filepath.Base(filepath.Dir(d)) == ".claude"
}

// walkTree walks root once, collecting directories that carry a .git entry
// (working trees, to resolve into repos) and every ".claude/worktrees"
// directory. It reports walk progress to r.
func walkTree(root string, r *reporter) (gitDirs, claudeDirs []string, err error) {
	dirs := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil // unreadable entry: skip, keep walking
		}
		if !d.IsDir() {
			return nil
		}
		dirs++
		if dirs%128 == 0 {
			r.progress("scanning %s  (%d dirs)", root, dirs)
		}
		if skipDirs[d.Name()] {
			return fs.SkipDir
		}
		if d.Name() == "worktrees" && filepath.Base(filepath.Dir(p)) == ".claude" {
			claudeDirs = append(claudeDirs, p)
			return fs.SkipDir // children handled separately; don't double-count
		}
		if hasGitEntry(p) {
			gitDirs = append(gitDirs, p)
		}
		return nil
	})
	return gitDirs, claudeDirs, err
}

// repoRootOf turns a common git dir into the repo's working-tree root.
func repoRootOf(commonDir string) string {
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return commonDir // bare repo
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// orphanRepoRoot best-effort recovers the repo an orphan worktree belonged to,
// by reading its .git file, then falling back to the .claude/worktrees layout.
func orphanRepoRoot(worktreeDir string) string {
	if b, err := os.ReadFile(filepath.Join(worktreeDir, ".git")); err == nil {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
		if i := strings.Index(s, "/.git/worktrees/"); i >= 0 {
			return s[:i]
		}
	}
	if underClaudeWorktrees(worktreeDir) {
		return filepath.Dir(filepath.Dir(filepath.Dir(worktreeDir)))
	}
	return ""
}

// discoverWorktrees walks root and returns every linked worktree it can find,
// from three sources unioned together: directories under .claude/worktrees,
// `git worktree list` of every repo found, and abandoned worktrees (orphan
// directories and stale git entries). Discovery fields are filled in; merge
// analysis is left to analyze. Progress is reported to r.
func discoverWorktrees(root string, r *reporter) ([]Worktree, error) {
	r.progress("scanning %s", root)
	gitDirs, claudeDirs, err := walkTree(root, r)
	if err != nil {
		return nil, err
	}

	repos := map[string]string{} // common git dir -> a working dir inside that repo
	orphans := map[string]bool{} // worktree dirs whose backing git dir is gone

	consider := func(dir string) {
		if cd, ok := commonGitDir(dir); ok {
			if _, seen := repos[cd]; !seen {
				repos[cd] = dir
			}
			return
		}
		orphans[dir] = true // a .git entry that no longer resolves
	}
	for _, d := range gitDirs {
		consider(d)
	}

	// .claude/worktrees children: the walk does not descend into them.
	claudeChildren := map[string]bool{}
	for _, cwd := range claudeDirs {
		ents, e := os.ReadDir(cwd)
		if e != nil {
			continue
		}
		for _, ent := range ents {
			if !ent.IsDir() {
				continue
			}
			child := filepath.Join(cwd, ent.Name())
			if !hasGitEntry(child) {
				continue // a plain directory / committed fixture, not a worktree
			}
			abs, _ := filepath.Abs(child)
			claudeChildren[abs] = true
			consider(child)
		}
	}

	result := map[string]*Worktree{}

	// Source: `git worktree list` of every repo found.
	r.progress("listing worktrees in %d repo(s)", len(repos))
	for cd, anyDir := range repos {
		entries, e := listWorktrees(anyDir)
		if e != nil {
			r.note("warning: git worktree list failed in %s: %v", repoRootOf(cd), e)
			continue
		}
		repoRoot := repoRootOf(cd)
		r.note("repo %s: %d linked worktree(s)", repoRoot, len(entries))
		for _, en := range entries {
			if en.Bare {
				continue
			}
			abs, _ := filepath.Abs(en.Path)
			result[abs] = &Worktree{
				Path: abs, Name: filepath.Base(abs), Repo: repoRoot,
				Branch: en.Branch, Head: short(en.Head), Detached: en.Detached,
				Registered: true, Locked: en.Locked, LockReason: en.LockReason,
				GitPrunable: en.Prunable, OnDisk: dirExists(abs), SizeBytes: -1,
			}
		}
	}

	// Source: orphan worktree directories (on disk, git no longer tracks them).
	for od := range orphans {
		abs, _ := filepath.Abs(od)
		if _, ok := result[abs]; ok {
			continue
		}
		result[abs] = &Worktree{
			Path: abs, Name: filepath.Base(abs), Repo: orphanRepoRoot(abs),
			OnDisk: true, SizeBytes: -1,
		}
	}

	// Tag worktrees that are Claude Code worktrees.
	for abs := range claudeChildren {
		if w, ok := result[abs]; ok {
			w.Claude = true
		}
	}
	for p, w := range result {
		if underClaudeWorktrees(p) {
			w.Claude = true
		}
	}

	out := make([]Worktree, 0, len(result))
	for _, w := range result {
		w.Kind = kindOf(w)
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	r.note("discovered %d worktree(s)", len(out))
	return out, nil
}

// analyze fills in the merge analysis for a worktree discovered by
// discoverWorktrees. withSize controls whether disk usage is measured.
// baseOverride, when non-empty, forces the ref the merge check runs against.
// Abandoned worktrees carry no merge analysis — there is nothing to integrate.
//
// A live worktree counts as merged if its HEAD is an ancestor of its base, OR
// if HEAD is contained in some branch other than its own. The base is
// recovered per-worktree (reflog, then name-rev, then upstream); when it
// cannot be recovered the base is left blank but the contains check still
// gives a verdict.
func analyze(w *Worktree, withSize bool, baseOverride string) {
	if withSize && w.OnDisk {
		if sz, err := dirSize(w.Path); err == nil {
			w.SizeBytes = sz
		}
	}
	if w.Kind != "live" {
		return
	}
	if !isLinkedWorktree(w.Path) {
		w.Err = "not a git worktree"
		return
	}

	head, err := headOf(w.Path)
	if err != nil {
		w.Err = err.Error()
		return
	}
	w.Head = short(head)
	w.Branch = branchOf(w.Path)
	w.Detached = w.Branch == ""
	w.Dirty = isDirty(w.Path)

	name, ref, from, baseErr := resolveBase(w.Repo, w.Branch, baseOverride)
	if baseErr != nil && baseOverride != "" {
		w.Err = baseErr.Error() // a user-supplied base must resolve
		return
	}
	if baseErr == nil {
		w.Base, w.BaseFrom = name, from
		merged, e := isMerged(w.Repo, head, ref)
		if e != nil {
			w.Err = e.Error()
			return
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
}

// resolveBase picks the ref to test a worktree branch against. It returns a
// display name, a resolvable git ref, and how the choice was made:
//
//	flag       - the explicit --base override
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
