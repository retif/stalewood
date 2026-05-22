package main

import "testing"

// FuzzParseLintGroups feeds arbitrary selector strings to the lint parser; it
// must never panic — only return a value or an error — and a parsed selector
// must be usable.
func FuzzParseLintGroups(f *testing.F) {
	for _, s := range []string{"merged", "merged,!claude", "abandoned", "", "  ,  ", "!!x", "a,b,c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		groups, err := parseLintGroups([]string{s})
		if err != nil {
			return
		}
		matchLint(groups, Worktree{Kind: "live"})
	})
}

// FuzzLockOwnerPID feeds arbitrary lock-reason strings to the PID parser.
func FuzzLockOwnerPID(f *testing.F) {
	for _, s := range []string{"claude agent x (pid 123)", "(pid )", "(pid abc)", "", "(pid 9)x(pid 8)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		pid, ok := lockOwnerPID(s)
		if ok && pid <= 0 {
			t.Fatalf("lockOwnerPID(%q) ok with non-positive pid %d", s, pid)
		}
	})
}
