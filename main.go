// Command stale-worktrees scans a directory tree for Claude Code worktrees
// (".claude/worktrees/*") and reports, for each one, whether its branch has
// already been merged into the branch it was forked from. With -prune it
// removes the worktrees that are fully merged.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
)

func main() {
	prune := flag.Bool("prune", false, "remove worktrees whose branch is merged into its base")
	force := flag.Bool("force", false, "with -prune, also remove merged worktrees that have uncommitted changes")
	jsonOut := flag.Bool("json", false, "emit JSON instead of a table")
	withSize := flag.Bool("size", false, "measure each worktree's disk usage")
	base := flag.String("base", "", "ref to test every worktree against (default: per-worktree base)")
	flag.Usage = usage
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fatal(err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		fatal(fmt.Errorf("%s: not a directory", root))
	}

	paths, err := collectWorktrees(abs)
	if err != nil {
		fatal(err)
	}

	// Pruning needs sizes so it can report reclaimed space.
	measure := *withSize || *prune
	wts := make([]Worktree, len(paths))
	for i, p := range paths {
		wts[i] = analyze(p, measure, *base)
	}

	if *prune {
		os.Exit(runPrune(abs, wts, *force, *jsonOut))
	}
	if *jsonOut {
		emitJSON(abs, wts)
		return
	}
	emitTable(abs, wts, *withSize)
}

func usage() {
	fmt.Fprint(os.Stderr, `stale-worktrees - find and reap merged Claude Code worktrees

Usage:
  stale-worktrees [flags] [path]

  path   directory tree to scan (default ".")

Each worktree's branch is tested against the branch it was forked from
(recovered from the reflog). When the base cannot be recovered, the repo's
main/master branch is used instead.

Flags:
  -prune        remove worktrees whose branch is merged into its base
  -force        with -prune, also remove merged worktrees with uncommitted changes
  -size         measure each worktree's disk usage
  -base REF     test every worktree against REF instead of its own base
  -json         emit JSON instead of a table

The BASE column shows the ref used; "(auto)" means the base could not be
recovered and main/master was used, "(flag)" means -base was given.

Examples:
  stale-worktrees ~/projects               # report
  stale-worktrees -size ~/projects          # report with disk usage
  stale-worktrees -base oleks/main ~/repo   # force a specific base
  stale-worktrees -prune ~/projects         # remove merged worktrees
`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "stale-worktrees:", err)
	os.Exit(1)
}

// repoLabel renders a repo path relative to the scan root for compact display.
func repoLabel(root, repo string) string {
	if rel, err := filepath.Rel(root, repo); err == nil && rel != "." {
		return rel
	}
	return filepath.Base(repo)
}

// baseLabel renders the base ref plus a marker when it was not the worktree's
// own recovered base.
func baseLabel(w Worktree) string {
	if w.Base == "" {
		return "-"
	}
	switch w.BaseFrom {
	case "auto":
		return w.Base + " (auto)"
	case "flag":
		return w.Base + " (flag)"
	default:
		return w.Base
	}
}

func emitTable(root string, wts []Worktree, withSize bool) {
	if len(wts) == 0 {
		fmt.Println("No .claude/worktrees found under", root)
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	header := "REPO\tWORKTREE\tBRANCH\tBASE\tSTATUS"
	if withSize {
		header += "\tSIZE"
	}
	fmt.Fprintln(tw, header)

	var merged, unmerged, errored int
	var reclaimable int64
	for _, w := range wts {
		switch {
		case w.Err != "":
			errored++
		case w.Merged:
			merged++
			if w.SizeBytes > 0 {
				reclaimable += w.SizeBytes
			}
		default:
			unmerged++
		}
		branch := w.Branch
		if w.Detached {
			branch = "(detached)"
		}
		status := w.Status()
		if w.Err != "" {
			status = "error: " + w.Err
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s",
			repoLabel(root, w.Repo), w.Name, branch, baseLabel(w), status)
		if withSize {
			row += "\t" + humanSize(w.SizeBytes)
		}
		fmt.Fprintln(tw, row)
	}
	tw.Flush()

	fmt.Printf("\n%d worktree(s) in %d repo(s) - %d merged - %d unmerged",
		len(wts), countRepos(wts), merged, unmerged)
	if errored > 0 {
		fmt.Printf(" - %d error", errored)
	}
	fmt.Println()
	if merged > 0 {
		hint := ""
		if reclaimable > 0 {
			hint = fmt.Sprintf(" (~%s)", humanSize(reclaimable))
		}
		fmt.Printf("%d worktree(s) are merged and removable%s - run with -prune to reap them.\n", merged, hint)
	}
}

func countRepos(wts []Worktree) int {
	seen := map[string]bool{}
	for _, w := range wts {
		seen[w.Repo] = true
	}
	return len(seen)
}

type jsonReport struct {
	Root      string     `json:"root"`
	Count     int        `json:"count"`
	Worktrees []Worktree `json:"worktrees"`
}

func emitJSON(root string, wts []Worktree) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(jsonReport{Root: root, Count: len(wts), Worktrees: wts})
}

type pruneAction struct {
	Path   string `json:"path"`
	Repo   string `json:"repo"`
	Name   string `json:"name"`
	Action string `json:"action"` // removed | skipped | failed | kept
	Reason string `json:"reason,omitempty"`
	Freed  int64  `json:"freed_bytes,omitempty"`
}

// runPrune removes every merged worktree and returns a process exit code.
func runPrune(root string, wts []Worktree, force, jsonOut bool) int {
	var actions []pruneAction
	var freed int64
	var removed, skipped, failed, kept int

	for _, w := range wts {
		a := pruneAction{Path: w.Path, Repo: w.Repo, Name: w.Name}
		switch {
		case !w.Prunable():
			a.Action = "kept"
			if w.Err != "" {
				a.Reason = w.Err
			} else {
				a.Reason = "not merged"
			}
			kept++
		case w.Dirty && !force:
			a.Action = "skipped"
			a.Reason = "uncommitted changes (rerun with -force)"
			skipped++
		default:
			if err := removeWorktree(w.Repo, w.Path, w.Dirty); err != nil {
				a.Action = "failed"
				a.Reason = err.Error()
				failed++
			} else {
				a.Action = "removed"
				if w.SizeBytes > 0 {
					a.Freed = w.SizeBytes
					freed += w.SizeBytes
				}
				removed++
			}
		}
		actions = append(actions, a)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(struct {
			Root       string        `json:"root"`
			Removed    int           `json:"removed"`
			Skipped    int           `json:"skipped"`
			Failed     int           `json:"failed"`
			Kept       int           `json:"kept"`
			FreedBytes int64         `json:"freed_bytes"`
			Actions    []pruneAction `json:"actions"`
		}{root, removed, skipped, failed, kept, freed, actions})
	} else {
		for _, a := range actions {
			if a.Action == "kept" {
				continue // unmerged worktrees are the normal case; stay quiet
			}
			line := fmt.Sprintf("  %-8s %s/%s", a.Action, repoLabel(root, a.Repo), a.Name)
			if a.Reason != "" {
				line += "  (" + a.Reason + ")"
			}
			fmt.Println(line)
		}
		fmt.Printf("\nremoved %d - skipped %d - failed %d - kept %d unmerged",
			removed, skipped, failed, kept)
		if freed > 0 {
			fmt.Printf(" - reclaimed ~%s", humanSize(freed))
		}
		fmt.Println()
	}

	if failed > 0 {
		return 1
	}
	return 0
}

// humanSize renders a byte count; negative means "not measured".
func humanSize(b int64) string {
	if b < 0 {
		return "-"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}
