package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// stringList accumulates repeated flag values; the standard flag package keeps
// only the last value of a repeated flag without a custom flag.Value.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, " ") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// predicates maps a lint predicate name to a test over a worktree's state.
var predicates = map[string]func(Worktree) bool{
	"merged":       func(w Worktree) bool { return w.Merged },
	"unmerged":     func(w Worktree) bool { return w.Kind == "live" && w.Err == "" && !w.Merged },
	"live":         func(w Worktree) bool { return w.Kind == "live" },
	"abandoned":    func(w Worktree) bool { return w.Kind != "live" },
	"orphan":       func(w Worktree) bool { return w.Kind == "abandoned-orphan" },
	"stale":        func(w Worktree) bool { return w.Kind == "abandoned-stale" },
	"dirty":        func(w Worktree) bool { return w.Dirty },
	"modified":     func(w Worktree) bool { return w.Modified },
	"untracked":    func(w Worktree) bool { return w.Untracked },
	"locked":       func(w Worktree) bool { return w.Locked },
	"lock-stale":   func(w Worktree) bool { return w.LockStale() },
	"claude":       func(w Worktree) bool { return w.Claude },
	"manual":       func(w Worktree) bool { return !w.Claude },
	"detached":     func(w Worktree) bool { return w.Detached },
	"error":        func(w Worktree) bool { return w.Err != "" },
	"git-prunable": func(w Worktree) bool { return w.GitPrunable },
	"removable":    func(w Worktree) bool { return w.Prunable() },
	"any":          func(w Worktree) bool { return true },
}

// predicateNames returns the known predicate names, sorted.
func predicateNames() []string {
	names := make([]string, 0, len(predicates))
	for n := range predicates {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// lintTerm is one predicate, optionally negated with a leading "!".
type lintTerm struct {
	pred string
	neg  bool
}

// lintGroup is an AND of terms — a single --lint value.
type lintGroup []lintTerm

// match reports whether w satisfies every term in the group.
func (g lintGroup) match(w Worktree) bool {
	for _, t := range g {
		got := predicates[t.pred](w)
		if t.neg {
			got = !got
		}
		if !got {
			return false
		}
	}
	return len(g) > 0
}

// parseLintGroups parses repeated --lint values into OR-ed AND-groups. Each
// value is a comma-separated list of predicate names; a "!" prefix negates.
func parseLintGroups(args []string) ([]lintGroup, error) {
	var groups []lintGroup
	for _, arg := range args {
		var g lintGroup
		for _, tok := range strings.Split(arg, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			neg := strings.HasPrefix(tok, "!")
			name := strings.TrimSpace(strings.TrimPrefix(tok, "!"))
			if _, ok := predicates[name]; !ok {
				return nil, fmt.Errorf("unknown lint predicate %q (known: %s)",
					name, strings.Join(predicateNames(), " "))
			}
			g = append(g, lintTerm{pred: name, neg: neg})
		}
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("empty lint selector")
	}
	return groups, nil
}

// matchLint reports whether w matches any group (OR of the AND-groups).
func matchLint(groups []lintGroup, w Worktree) bool {
	for _, g := range groups {
		if g.match(w) {
			return true
		}
	}
	return false
}

// discoverRepo enumerates the worktrees of the single git repo containing
// path — fast, with no directory-tree walk — for lint mode. It returns the
// worktrees and the repo root.
func discoverRepo(path string) ([]Worktree, string, error) {
	cd, ok := commonGitDir(path)
	if !ok {
		return nil, "", fmt.Errorf("%s: not inside a git repository", path)
	}
	repoRoot := repoRootOf(cd)
	entries, err := listWorktrees(path)
	if err != nil {
		return nil, "", err
	}
	result := map[string]*Worktree{}
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
	// Orphan .claude/worktrees children that git no longer tracks.
	cwDir := filepath.Join(repoRoot, ".claude", "worktrees")
	if ents, e := os.ReadDir(cwDir); e == nil {
		for _, ent := range ents {
			if !ent.IsDir() {
				continue
			}
			child := filepath.Join(cwDir, ent.Name())
			abs, _ := filepath.Abs(child)
			if _, exists := result[abs]; exists || !hasGitEntry(child) {
				continue
			}
			result[abs] = &Worktree{
				Path: abs, Name: filepath.Base(abs), Repo: orphanRepoRoot(abs),
				OnDisk: true, SizeBytes: -1,
			}
		}
	}
	out := make([]Worktree, 0, len(result))
	for p, w := range result {
		if underClaudeWorktrees(p) {
			w.Claude = true
		}
		w.Kind = kindOf(w)
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, repoRoot, nil
}

// runLint applies the lint groups to a repo's worktrees, reports the ones that
// match, and returns the process exit code: 1 if any matched, else 0.
func runLint(repoRoot string, wts []Worktree, groups []lintGroup, jsonOut bool, pal palette) int {
	var matched []Worktree
	for _, w := range wts {
		if matchLint(groups, w) {
			matched = append(matched, w)
		}
	}
	if jsonOut {
		emitJSON(repoRoot, matched)
		if len(matched) > 0 {
			return 1
		}
		return 0
	}
	if len(matched) == 0 {
		return 0 // clean: stay silent (clig: quiet when nothing is wrong)
	}
	fmt.Printf("stalewood: %d of %d worktree(s) in %s flagged by --lint\n",
		len(matched), len(wts), filepath.Base(repoRoot))
	for _, w := range matched {
		verdict := verdictText(w)
		if w.Merged && w.MergedInto != "" && w.MergedInto != w.Base {
			verdict += " -> " + w.MergedInto
		}
		line := fmt.Sprintf("  %s %s  %s",
			paintSeverity(pal, w, glyph(w)), pal.bold(w.Name), paintSeverity(pal, w, verdict))
		for _, t := range worktreeTags(w) {
			line += " " + paintTag(pal, t)
		}
		fmt.Println(line)
		fmt.Println("    " + pal.dim(w.Path))
	}
	return 1
}
