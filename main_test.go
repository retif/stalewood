package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

func TestWorktreeTags(t *testing.T) {
	cases := []struct {
		w    Worktree
		want string
	}{
		{Worktree{}, ""},
		{Worktree{Claude: true}, "claude"},
		{Worktree{Modified: true}, "modified files"},
		{Worktree{Untracked: true}, "untracked files"},
		{Worktree{Modified: true, Untracked: true}, "modified files untracked files"},
		{Worktree{Claude: true, Locked: true}, "claude locked"},
		{Worktree{Claude: true, Locked: true, LockReason: "x (pid 2147483646)"}, "claude lock-stale"},
		{Worktree{GitPrunable: true}, "git-prunable"},
		{Worktree{Claude: true, Untracked: true, GitPrunable: true}, "claude untracked files git-prunable"},
	}
	for i, c := range cases {
		if got := strings.Join(worktreeTags(c.w), " "); got != c.want {
			t.Errorf("case %d: worktreeTags = %q, want %q", i, got, c.want)
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
		{Worktree{Kind: "live", Claude: true}, "unmerged [claude]"},
		{Worktree{Kind: "live", Untracked: true}, "unmerged [untracked files]"},
		{Worktree{Kind: "live", Modified: true, Locked: true}, "unmerged [modified files] [locked]"},
		{Worktree{Kind: "abandoned-orphan"}, "abandoned (orphan dir)"},
		{Worktree{Kind: "abandoned-stale", GitPrunable: true}, "abandoned (stale entry) [git-prunable]"},
		{Worktree{Err: "boom"}, "error: boom"},
	}
	for i, c := range cases {
		if got := statusLabel(c.w); got != c.want {
			t.Errorf("case %d: statusLabel = %q, want %q", i, got, c.want)
		}
	}
}

func TestGlyph(t *testing.T) {
	cases := []struct {
		w    Worktree
		want string
	}{
		{Worktree{Kind: "live", Merged: true}, "✓"},
		{Worktree{Kind: "live"}, "✗"},
		{Worktree{Kind: "abandoned-orphan"}, "⚠"},
		{Worktree{Kind: "live", Err: "boom"}, "!"},
	}
	for i, c := range cases {
		if got := glyph(c.w); got != c.want {
			t.Errorf("case %d: glyph = %q, want %q", i, got, c.want)
		}
	}
}

func TestLockOwnerPID(t *testing.T) {
	cases := []struct {
		reason string
		pid    int
		ok     bool
	}{
		{"claude agent agent-x (pid 2685793)", 2685793, true},
		{"locked by hand", 0, false},
		{"", 0, false},
		{"weird (pid notanumber)", 0, false},
	}
	for i, c := range cases {
		pid, ok := lockOwnerPID(c.reason)
		if ok != c.ok || pid != c.pid {
			t.Errorf("case %d: lockOwnerPID(%q) = %d,%v want %d,%v", i, c.reason, pid, ok, c.pid, c.ok)
		}
	}
}

func TestLockStale(t *testing.T) {
	// pid 1 (init) is always alive; a pid far above the kernel maximum never exists.
	if (Worktree{Locked: true, LockReason: "x (pid 1)"}).LockStale() {
		t.Errorf("lock owned by pid 1 reported stale")
	}
	if !(Worktree{Locked: true, LockReason: "x (pid 2147483646)"}).LockStale() {
		t.Errorf("lock owned by a non-existent pid not reported stale")
	}
	if (Worktree{Locked: true, LockReason: "locked by hand"}).LockStale() {
		t.Errorf("lock with no pid reported stale")
	}
	if (Worktree{Locked: false}).LockStale() {
		t.Errorf("unlocked worktree reported stale")
	}
}

func TestPalette(t *testing.T) {
	off := palette{enabled: false}
	for _, s := range []string{off.green("x"), off.bold("x"), off.dim("x"), off.cyan("x")} {
		if s != "x" {
			t.Errorf("disabled palette coloured output: %q", s)
		}
	}
	on := palette{enabled: true}
	if got := on.bold("x"); got == "x" || !strings.Contains(got, "x") {
		t.Errorf("enabled palette did not wrap: %q", got)
	}
	if got := on.green(""); got != "" {
		t.Errorf("palette coloured an empty string: %q", got)
	}
}

func TestPaintSeverity(t *testing.T) {
	pal := palette{enabled: false} // disabled palette: paintSeverity is a pass-through
	for i, w := range []Worktree{
		{Kind: "live", Merged: true},
		{Kind: "abandoned-orphan"},
		{Kind: "live", Err: "boom"},
		{Kind: "live"},
	} {
		if got := paintSeverity(pal, w, "X"); got != "X" {
			t.Errorf("case %d: paintSeverity = %q, want X", i, got)
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
	wts, err := discoverWorktrees(root, nil)
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
// unmerged worktree with an untracked file, and verifies detection end to end.
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
	if err := os.WriteFile(filepath.Join(unmergedWT, "stray.txt"), []byte("y\n"), 0o644); err != nil {
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
	if u.Merged || u.Prunable() {
		t.Errorf("unmerged: Merged=%v Prunable=%v", u.Merged, u.Prunable())
	}
	if !u.Untracked || u.Modified || !u.Dirty {
		t.Errorf("unmerged: Untracked=%v Modified=%v Dirty=%v, want true/false/true",
			u.Untracked, u.Modified, u.Dirty)
	}
}

// TestModifiedVsUntracked verifies a tracked-file edit is reported as Modified,
// not Untracked.
func TestModifiedVsUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	wt := filepath.Join(mkClaudeWorktrees(t, repo), "w")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "w", wt, "main")
	// Edit a tracked file (README exists from initRepo).
	if err := os.WriteFile(filepath.Join(wt, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := discoverAnalyzed(t, root, "")["w"]
	if !w.Modified || w.Untracked {
		t.Errorf("Modified=%v Untracked=%v, want true/false", w.Modified, w.Untracked)
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

// TestBaseOverride verifies --base forces the ref and marks BaseFrom as "flag".
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
	wts, err := discoverWorktrees(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 0 {
		t.Fatalf("plain dir without .git should be skipped, got %v", wts)
	}
}

// TestLockedWorktree verifies a real `git worktree lock` is detected, with its
// reason captured.
func TestLockedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	wt := filepath.Join(mkClaudeWorktrees(t, repo), "held")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "held", wt, "main")
	run(t, repo, "git", "worktree", "lock", "--reason", "test hold (pid 1)", wt)

	w := discoverAnalyzed(t, root, "")["held"]
	if !w.Locked {
		t.Fatalf("locked worktree not reported as locked")
	}
	if !strings.Contains(w.LockReason, "test hold") {
		t.Errorf("LockReason = %q, want it to contain the reason", w.LockReason)
	}
	if w.LockStale() {
		t.Errorf("lock owned by pid 1 reported stale")
	}
}

// TestJSONSchema checks that the published --json-schema is valid JSON and that
// its worktree property set matches the Worktree struct's json tags, so the
// schema cannot silently drift from the data.
func TestJSONSchema(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(jsonSchema), &doc); err != nil {
		t.Fatalf("jsonSchema is not valid JSON: %v", err)
	}
	defs, _ := doc["$defs"].(map[string]any)
	wt, _ := defs["worktree"].(map[string]any)
	props, _ := wt["properties"].(map[string]any)
	if len(props) == 0 {
		t.Fatal("jsonSchema: $defs.worktree.properties missing or empty")
	}
	declared := map[string]bool{}
	for k := range props {
		declared[k] = true
	}
	rt := reflect.TypeOf(Worktree{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if !declared[name] {
			t.Errorf("Worktree.%s (json %q) is missing from jsonSchema", rt.Field(i).Name, name)
		}
		delete(declared, name)
	}
	for k := range declared {
		t.Errorf("jsonSchema declares worktree property %q with no matching struct field", k)
	}
}

func TestParseLintGroups(t *testing.T) {
	if _, err := parseLintGroups([]string{"merged,untracked", "abandoned"}); err != nil {
		t.Errorf("valid selector rejected: %v", err)
	}
	if _, err := parseLintGroups([]string{"merged,bogus"}); err == nil {
		t.Errorf("unknown predicate accepted")
	}
	if _, err := parseLintGroups([]string{"", "  "}); err == nil {
		t.Errorf("empty selector accepted")
	}
}

func TestMatchLint(t *testing.T) {
	merged := Worktree{Kind: "live", Merged: true}
	claudeWIP := Worktree{Kind: "live", Claude: true, Untracked: true}
	orphan := Worktree{Kind: "abandoned-orphan"}

	mustParse := func(args ...string) []lintGroup {
		g, err := parseLintGroups(args)
		if err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return g
	}

	g := mustParse("merged") // single predicate
	if !matchLint(g, merged) || matchLint(g, orphan) {
		t.Errorf("merged: matched the wrong worktrees")
	}
	g = mustParse("claude,!merged") // AND + NOT
	if !matchLint(g, claudeWIP) || matchLint(g, merged) {
		t.Errorf("claude,!merged: matched the wrong worktrees")
	}
	g = mustParse("abandoned", "merged") // OR across groups
	if !matchLint(g, orphan) || !matchLint(g, merged) || matchLint(g, claudeWIP) {
		t.Errorf("abandoned OR merged: matched the wrong worktrees")
	}
	g = mustParse("untracked,manual") // claudeWIP is untracked but not manual
	if matchLint(g, claudeWIP) {
		t.Errorf("untracked,manual matched a claude worktree")
	}
}

func TestDiscoverRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repo := initRepo(t, root, "r")
	wt := filepath.Join(mkClaudeWorktrees(t, repo), "w")
	run(t, repo, "git", "worktree", "add", "-q", "-b", "w", wt, "main")

	wts, repoRoot, err := discoverRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(repoRoot) != "r" {
		t.Errorf("repoRoot = %q, want basename r", repoRoot)
	}
	if len(wts) != 1 || wts[0].Name != "w" {
		t.Fatalf("discoverRepo found %d worktrees, want 1 (w): %v", len(wts), wts)
	}
	if !wts[0].Claude || !wts[0].Registered {
		t.Errorf("worktree: claude=%v registered=%v, want true/true", wts[0].Claude, wts[0].Registered)
	}
	if _, _, err := discoverRepo(root); err == nil {
		t.Errorf("discoverRepo on a non-repo dir should error")
	}
}
