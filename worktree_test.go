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
		{-1, "-"}, {0, "0B"}, {512, "512B"}, {1024, "1.0K"},
		{1536, "1.5K"}, {5 * 1024 * 1024, "5.0M"}, {3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrunable(t *testing.T) {
	cases := []struct {
		w    Worktree
		want bool
	}{
		{Worktree{Kind: "live", Merged: true}, true},
		{Worktree{Kind: "live", Merged: true, Dirty: true}, true},
		{Worktree{Kind: "live"}, false},
		{Worktree{Kind: "live", Merged: true, Err: "boom"}, false},
		{Worktree{Kind: "abandoned-orphan", Merged: true}, false},
		{Worktree{Kind: "abandoned-stale", Merged: true}, false},
	}
	for i, c := range cases {
		if got := c.w.Prunable(); got != c.want {
			t.Errorf("case %d: Prunable() = %v, want %v", i, got, c.want)
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
		{Worktree{Kind: "live", Merged: true, Base: "main", MergedInto: "main"}, "merged"},
		{Worktree{Kind: "live", Merged: true, Base: "main", MergedInto: "oleks/main"}, "merged -> oleks/main"},
		{Worktree{Kind: "live"}, "unmerged"},
		{Worktree{Kind: "live", Locked: true}, "unmerged [locked]"},
		{Worktree{Kind: "abandoned-orphan"}, "abandoned (orphan dir)"},
		{Worktree{Kind: "abandoned-stale"}, "abandoned (stale entry)"},
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

func mkClaudeWorktrees(t *testing.T, repo string) string {
	t.Helper()
	d := filepath.Join(repo, ".claude", "worktrees")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// discoverAnalyzed discovers worktrees under root, runs analysis on each, and
// returns them keyed by worktree name.
func discoverAnalyzed(t *testing.T, root, base string) map[string]Worktree {
	t.Helper()
	wts, err := discoverWorktrees(root)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Worktree{}
	for i := range wts {
		analyze(&wts[i], false, base)
		m[wts[i].Name] = wts[i]
	}
	return m
}

// TestScanAndAnalyze builds a repo with a merged worktree and an
// unmerged-dirty worktree and verifies detection end to end.
func TestScanAndAnalyze(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "myrepo")
	wtDir := mkClaudeWorktrees(t, repo)

	mergedWT := filepath.Join(wtDir, "merged")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat-merged", mergedWT, "main")

	unmergedWT := filepath.Join(wtDir, "unmerged")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat-unmerged", unmergedWT, "main")
	commit(t, unmergedWT, "new.txt", "ahead")
	if err := os.WriteFile(filepath.Join(unmergedWT, "dirty.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := discoverAnalyzed(t, root, "")
	if len(got) != 2 {
		t.Fatalf("discovered %d worktrees, want 2: %v", len(got), got)
	}

	m := got["merged"]
	if m.Err != "" {
		t.Fatalf("merged worktree errored: %s", m.Err)
	}
	if m.Kind != "live" || !m.Claude || !m.Registered || !m.OnDisk {
		t.Errorf("merged: kind=%q claude=%v registered=%v onDisk=%v",
			m.Kind, m.Claude, m.Registered, m.OnDisk)
	}
	if !m.Merged || m.Dirty || m.Branch != "feat-merged" {
		t.Errorf("merged: Merged=%v Dirty=%v Branch=%q", m.Merged, m.Dirty, m.Branch)
	}
	if m.Base != "main" || m.BaseFrom != "reflog" || m.MergedInto != "main" {
		t.Errorf("merged: base=%q from=%q into=%q, want main/reflog/main",
			m.Base, m.BaseFrom, m.MergedInto)
	}
	if !m.Prunable() {
		t.Errorf("merged: Prunable = false, want true")
	}

	u := got["unmerged"]
	if u.Err != "" {
		t.Fatalf("unmerged worktree errored: %s", u.Err)
	}
	if u.Merged || !u.Dirty || u.Prunable() {
		t.Errorf("unmerged: Merged=%v Dirty=%v Prunable=%v", u.Merged, u.Dirty, u.Prunable())
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
	wt := filepath.Join(mkClaudeWorktrees(t, repo), "w")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "wt", wt, "main")
	commit(t, wt, "f.txt", "wt work")
	run(t, repo, "git", "branch", "release", "wt")

	w := discoverAnalyzed(t, root, "")["w"]
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

// TestBaseFromCreationSHA verifies a branch created from a bare "HEAD" still
// has its base recovered, via the reflog creation SHA and name-rev.
func TestBaseFromCreationSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	wt := filepath.Join(mkClaudeWorktrees(t, repo), "w")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "fromhead", wt)
	commit(t, wt, "f.txt", "ahead")

	w := discoverAnalyzed(t, root, "")["w"]
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
	wt := filepath.Join(mkClaudeWorktrees(t, repo), "w")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "feat", wt, "main")

	w := discoverAnalyzed(t, root, "develop")["w"]
	if w.Err != "" {
		t.Fatalf("errored: %s", w.Err)
	}
	if w.Base != "develop" || w.BaseFrom != "flag" {
		t.Errorf("base = %q from %q, want develop from flag", w.Base, w.BaseFrom)
	}
}

// TestNonClaudeAndStale verifies worktrees outside .claude/worktrees are
// discovered via `git worktree list`, and that a worktree whose directory is
// removed becomes an abandoned stale entry.
func TestNonClaudeAndStale(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	side := filepath.Join(root, "side")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "side", side, "main")

	w := discoverAnalyzed(t, root, "")["side"]
	if w.Kind != "live" || w.Claude || !w.Registered || !w.OnDisk {
		t.Errorf("live side: kind=%q claude=%v registered=%v onDisk=%v",
			w.Kind, w.Claude, w.Registered, w.OnDisk)
	}

	// Remove the directory: git still tracks it -> abandoned stale entry.
	if err := os.RemoveAll(side); err != nil {
		t.Fatal(err)
	}
	w = discoverAnalyzed(t, root, "")["side"]
	if w.Kind != "abandoned-stale" || w.OnDisk {
		t.Errorf("stale side: kind=%q onDisk=%v, want abandoned-stale/false", w.Kind, w.OnDisk)
	}
}

// TestOrphanWorktree verifies a worktree directory whose backing git dir is
// gone is reported as an abandoned orphan, not an error.
func TestOrphanWorktree(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(mkClaudeWorktrees(t, filepath.Join(root, "repo")), "orphan")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// A .git file pointing at a git dir that does not exist.
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /nowhere/.git/worktrees/orphan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := discoverAnalyzed(t, root, "")
	w, ok := got["orphan"]
	if !ok {
		t.Fatalf("orphan worktree not discovered: %v", got)
	}
	if w.Kind != "abandoned-orphan" || w.Err != "" {
		t.Errorf("kind=%q err=%q, want abandoned-orphan / no error", w.Kind, w.Err)
	}
}

// TestPlainDirSkipped verifies a directory under .claude/worktrees with no
// .git entry (e.g. a committed test fixture) is not listed as a worktree.
func TestPlainDirSkipped(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "repo", ".claude", "worktrees", "notawt")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "plugin.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wts, err := discoverWorktrees(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 0 {
		t.Fatalf("plain dir without .git should be skipped, got %v", wts)
	}
}
