package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{-1, "-"},
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{5 * 1024 * 1024, "5.0M"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatusAndPrunable(t *testing.T) {
	cases := []struct {
		w        Worktree
		status   string
		prunable bool
	}{
		{Worktree{Merged: true}, "merged", true},
		{Worktree{Merged: true, Dirty: true}, "merged*", true},
		{Worktree{}, "unmerged", false},
		{Worktree{Dirty: true}, "unmerged*", false},
		{Worktree{Err: "boom"}, "error", false},
		{Worktree{Merged: true, Err: "boom"}, "error", false},
	}
	for i, c := range cases {
		if got := c.w.Status(); got != c.status {
			t.Errorf("case %d: Status() = %q, want %q", i, got, c.status)
		}
		if got := c.w.Prunable(); got != c.prunable {
			t.Errorf("case %d: Prunable() = %v, want %v", i, got, c.prunable)
		}
	}
}

func TestBaseLabel(t *testing.T) {
	cases := []struct {
		w    Worktree
		want string
	}{
		{Worktree{Base: "origin/main", BaseFrom: "reflog"}, "origin/main"},
		{Worktree{Base: "main", BaseFrom: "reflog-sha"}, "main (sha)"},
		{Worktree{Base: "develop", BaseFrom: "upstream"}, "develop (upstream)"},
		{Worktree{Base: "main", BaseFrom: "auto"}, "main (auto)"},
		{Worktree{Base: "develop", BaseFrom: "flag"}, "develop (flag)"},
		{Worktree{Base: ""}, "-"},
	}
	for i, c := range cases {
		if got := baseLabel(c.w); got != c.want {
			t.Errorf("case %d: baseLabel = %q, want %q", i, got, c.want)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	cases := []struct {
		w    Worktree
		want string
	}{
		{Worktree{Merged: true, Base: "main", MergedInto: "main"}, "merged"},
		{Worktree{Merged: true, Base: "main", MergedInto: "oleks/main"}, "merged -> oleks/main"},
		{Worktree{}, "unmerged"},
		{Worktree{Err: "boom"}, "error: boom"},
	}
	for i, c := range cases {
		if got := statusLabel(c.w); got != c.want {
			t.Errorf("case %d: statusLabel = %q, want %q", i, got, c.want)
		}
	}
}

// run executes a command in dir and fails the test on error.
func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// initRepo creates a git repo at <root>/<name> with one commit on main.
func initRepo(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "-m", "initial")
	return repo
}

func commit(t *testing.T, dir, file, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-q", "-m", msg)
}

// TestScanAndAnalyze builds a repo with a merged worktree and an
// unmerged-dirty worktree and verifies detection end to end.
func TestScanAndAnalyze(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "myrepo")
	wtDir := filepath.Join(repo, ".claude", "worktrees")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Merged worktree: forked from main, no commits ahead.
	mergedWT := filepath.Join(wtDir, "merged")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat-merged", mergedWT, "main")

	// Unmerged + dirty worktree: a commit ahead of main, plus an
	// uncommitted edit on top.
	unmergedWT := filepath.Join(wtDir, "unmerged")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat-unmerged", unmergedWT, "main")
	commit(t, unmergedWT, "new.txt", "ahead")
	if err := os.WriteFile(filepath.Join(unmergedWT, "dirty.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := collectWorktrees(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("collectWorktrees found %d worktrees, want 2: %v", len(paths), paths)
	}

	got := map[string]Worktree{}
	for _, p := range paths {
		w := analyze(p, true, "")
		got[w.Name] = w
	}

	m := got["merged"]
	if m.Err != "" {
		t.Fatalf("merged worktree errored: %s", m.Err)
	}
	if !m.Merged || m.Dirty {
		t.Errorf("merged worktree: Merged=%v Dirty=%v, want true/false", m.Merged, m.Dirty)
	}
	if m.Branch != "feat-merged" {
		t.Errorf("merged worktree: Branch = %q, want feat-merged", m.Branch)
	}
	if m.Base != "main" || m.BaseFrom != "reflog" || m.MergedInto != "main" {
		t.Errorf("merged worktree: base=%q from=%q into=%q, want main/reflog/main",
			m.Base, m.BaseFrom, m.MergedInto)
	}
	if !m.Prunable() {
		t.Errorf("merged worktree: Prunable = false, want true")
	}

	u := got["unmerged"]
	if u.Err != "" {
		t.Fatalf("unmerged worktree errored: %s", u.Err)
	}
	if u.Merged || !u.Dirty {
		t.Errorf("unmerged worktree: Merged=%v Dirty=%v, want false/true", u.Merged, u.Dirty)
	}
	if u.Prunable() {
		t.Errorf("unmerged worktree: Prunable = true, want false")
	}
}

// TestContainsMerge verifies that work integrated into a branch other than
// the worktree's base is still detected as merged.
func TestContainsMerge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	wt := filepath.Join(repo, ".claude", "worktrees", "w")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "worktree", "add", "-q", "-b", "wt", wt, "main")
	commit(t, wt, "f.txt", "wt work")
	// A separate branch that also contains the worktree's commit.
	run(t, repo, "git", "branch", "release", "wt")

	w := analyze(wt, false, "")
	if w.Err != "" {
		t.Fatalf("errored: %s", w.Err)
	}
	if w.Base != "main" {
		t.Errorf("Base = %q, want main", w.Base)
	}
	if !w.Merged || w.MergedInto != "release" {
		t.Errorf("Merged=%v MergedInto=%q, want true/release", w.Merged, w.MergedInto)
	}
}

// TestBaseFromCreationSHA verifies that a branch created from a bare "HEAD"
// still has its base recovered, via the reflog creation SHA and name-rev.
func TestBaseFromCreationSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	wt := filepath.Join(repo, ".claude", "worktrees", "w")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	// No start point: the branch is created from HEAD, so the reflog ref is
	// the unhelpful literal "HEAD" -- recovery must fall back to the SHA.
	run(t, repo, "git", "worktree", "add", "-q", "-b", "fromhead", wt)
	commit(t, wt, "f.txt", "ahead")

	w := analyze(wt, false, "")
	if w.Err != "" {
		t.Fatalf("errored: %s", w.Err)
	}
	if w.Base != "main" || w.BaseFrom != "reflog-sha" {
		t.Errorf("base = %q from %q, want main from reflog-sha", w.Base, w.BaseFrom)
	}
}

// TestBaseOverride verifies -base forces the ref and marks BaseFrom as "flag".
func TestBaseOverride(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	run(t, repo, "git", "branch", "develop")
	wt := filepath.Join(repo, ".claude", "worktrees", "w")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat", wt, "main")

	w := analyze(wt, false, "develop")
	if w.Err != "" {
		t.Fatalf("errored: %s", w.Err)
	}
	if w.Base != "develop" || w.BaseFrom != "flag" {
		t.Errorf("base = %q from %q, want develop from flag", w.Base, w.BaseFrom)
	}
}

// TestNonWorktreeDir verifies a plain directory under .claude/worktrees is
// reported as an error rather than crashing.
func TestNonWorktreeDir(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "repo", ".claude", "worktrees", "notawt")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := collectWorktrees(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("found %d, want 1", len(paths))
	}
	w := analyze(paths[0], false, "")
	if w.Err == "" {
		t.Errorf("expected error for non-worktree dir, got none")
	}
}
