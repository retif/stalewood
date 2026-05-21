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

// TestScanAndAnalyze builds a real repo with a merged worktree and an
// unmerged-dirty worktree, then verifies detection end to end.
func TestScanAndAnalyze(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	run(t, repo, "git", "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "-m", "initial")

	wtDir := filepath.Join(repo, ".claude", "worktrees")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Merged worktree: branch sits exactly at main, so HEAD is an ancestor.
	mergedWT := filepath.Join(wtDir, "merged")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat-merged", mergedWT, "main")

	// Unmerged + dirty worktree: a new commit ahead of main, plus an
	// uncommitted edit on top.
	unmergedWT := filepath.Join(wtDir, "unmerged")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat-unmerged", unmergedWT, "main")
	if err := os.WriteFile(filepath.Join(unmergedWT, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, unmergedWT, "git", "add", "-A")
	run(t, unmergedWT, "git", "commit", "-q", "-m", "ahead")
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
	if !m.Merged {
		t.Errorf("merged worktree: Merged = false, want true")
	}
	if m.Dirty {
		t.Errorf("merged worktree: Dirty = true, want false")
	}
	if m.Branch != "feat-merged" {
		t.Errorf("merged worktree: Branch = %q, want feat-merged", m.Branch)
	}
	if !m.Prunable() {
		t.Errorf("merged worktree: Prunable = false, want true")
	}

	u := got["unmerged"]
	if u.Err != "" {
		t.Fatalf("unmerged worktree errored: %s", u.Err)
	}
	if u.Merged {
		t.Errorf("unmerged worktree: Merged = true, want false")
	}
	if !u.Dirty {
		t.Errorf("unmerged worktree: Dirty = false, want true")
	}
	if u.Prunable() {
		t.Errorf("unmerged worktree: Prunable = true, want false")
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
