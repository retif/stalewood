// Command stalewood scans a directory tree for git worktrees — Claude Code
// worktrees under ".claude/worktrees/*", linked worktrees from
// `git worktree list`, and abandoned ones — and reports, for each, whether its
// work is already integrated. With --prune it removes the merged ones.
//
// Command-line behaviour follows https://clig.dev where reasonable; see
// CLAUDE.md for the project's CLI rules.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
)

const version = "0.1.0"

func main() {
	fs := flag.NewFlagSet("stalewood", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // usage and errors are printed by this file
	fs.Usage = func() {}

	prune := fs.Bool("prune", false, "remove worktrees whose work is merged")
	dryRun := fs.Bool("dry-run", false, "with --prune, show what would be removed without removing it")
	force := fs.Bool("force", false, "with --prune, also remove merged worktrees that are dirty or locked")
	jsonOut := fs.Bool("json", false, "emit JSON instead of a table")
	withSize := fs.Bool("size", false, "measure each worktree's disk usage")
	base := fs.String("base", "", "ref to test every worktree against (default: per-worktree base)")
	verbose := fs.Bool("verbose", false, "log per-worktree detail to stderr")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	noPager := fs.Bool("no-pager", false, "do not page long output")
	showVersion := fs.Bool("version", false, "print version and exit")

	switch err := fs.Parse(os.Args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		usage(os.Stdout) // explicit --help: stdout, exit 0
		os.Exit(0)
	case err != nil:
		fmt.Fprintln(os.Stderr, "stalewood:", err)
		usage(os.Stderr) // bad invocation: stderr, exit 2
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("stalewood", version)
		os.Exit(0)
	}

	root := "."
	switch fs.NArg() {
	case 0:
	case 1:
		root = fs.Arg(0)
	default:
		usageFail("accepts at most one path, got %d", fs.NArg())
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fatal(err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		usageFail("%s: not a directory", root)
	}

	r := newReporter(*verbose, *quiet)
	pal := newPalette()

	wts, err := discoverWorktrees(abs, r)
	if err != nil {
		r.clear()
		fatal(err)
	}

	// Pruning needs sizes so it can report reclaimed space.
	measure := *withSize || *prune
	for i := range wts {
		r.progress("analyzing %d/%d  %s", i+1, len(wts), wts[i].Name)
		analyze(&wts[i], measure, *base)
		r.note("%s/%s  base=%s  %s",
			repoLabel(abs, wts[i].Repo), wts[i].Name, baseLabel(wts[i]), statusLabel(wts[i]))
	}
	r.clear()

	if *dryRun && !*prune {
		r.note("note: --dry-run has no effect without --prune; the default report is already read-only")
	}

	switch {
	case *prune:
		os.Exit(runPrune(abs, wts, *force, *dryRun, *jsonOut, *noPager, pal))
	case *jsonOut:
		emitJSON(abs, wts)
	default:
		withPager(*noPager, func(w io.Writer) { emitTable(w, abs, wts, *withSize, pal) })
	}
}

// usage writes the help text to w — stdout on explicit --help, stderr on a
// bad invocation. It leads with examples (clig.dev).
func usage(w io.Writer) {
	fmt.Fprint(w, `stalewood - find and reap merged git worktrees

Usage:
  stalewood [flags] [path]

Examples:
  stalewood ~/projects                    report worktrees under a path
  stalewood --size ~/projects             report, measuring disk usage
  stalewood --prune --dry-run ~/projects  show what --prune would remove
  stalewood --prune ~/projects            remove every merged worktree

  path   directory tree to scan (default ".")

Worktrees are discovered from three sources: directories under
.claude/worktrees, git worktree list of every repo found, and abandoned
worktrees (orphan directories and stale entries). Running with no flags is a
read-only report - it shows exactly what --prune would remove.

Flags:
  --prune        remove worktrees whose work is merged
  --dry-run      with --prune, show what would be removed without removing it
  --force        with --prune, also remove merged worktrees that are dirty/locked
  --size         measure each worktree's disk usage
  --base REF     test every worktree against REF instead of its own base
  --json         emit JSON instead of a table
  --verbose      log per-worktree detail to stderr
  --quiet        suppress progress output
  --no-pager     do not page long output
  --version      print version and exit
  -h, --help     show this help

Exit codes:
  0  success
  1  runtime failure
  2  usage error
`)
}

// fatal reports a runtime failure on stderr and exits 1.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "stalewood:", err)
	os.Exit(1)
}

// usageFail reports a bad invocation on stderr, prints usage, and exits 2.
func usageFail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "stalewood: "+format+"\n", args...)
	usage(os.Stderr)
	os.Exit(2)
}

// repoLabel renders a repo path relative to the scan root for compact display.
func repoLabel(root, repo string) string {
	if repo == "" {
		return "?"
	}
	if rel, err := filepath.Rel(root, repo); err == nil && rel != "." {
		return rel
	}
	return filepath.Base(repo)
}

// baseLabel renders the base ref plus a marker for how it was recovered.
func baseLabel(w Worktree) string {
	if w.Base == "" {
		return "-"
	}
	switch w.BaseFrom {
	case "reflog-sha":
		return w.Base + " (sha)"
	case "upstream":
		return w.Base + " (upstream)"
	case "auto":
		return w.Base + " (auto)"
	case "flag":
		return w.Base + " (flag)"
	default: // reflog
		return w.Base
	}
}

// statusLabel renders the plain (uncoloured) STATUS text for a worktree.
func statusLabel(w Worktree) string {
	if w.Err != "" {
		return "error: " + w.Err
	}
	switch w.Kind {
	case "abandoned-orphan":
		return "abandoned (orphan dir)"
	case "abandoned-stale":
		return "abandoned (stale entry)"
	}
	s := w.Status()
	if w.Locked {
		s += " [locked]"
	}
	if w.Merged && w.MergedInto != "" && w.MergedInto != w.Base {
		s += " -> " + w.MergedInto
	}
	return s
}

// paintStatus colours a STATUS label: green merged, yellow abandoned, red error.
func paintStatus(pal palette, w Worktree, label string) string {
	switch {
	case w.Err != "":
		return pal.red(label)
	case w.Kind != "live":
		return pal.yellow(label)
	case w.Merged:
		return pal.green(label)
	default:
		return label
	}
}

// abandonedFix returns the suggested manual cleanup for an abandoned worktree.
func abandonedFix(w Worktree) string {
	switch w.Kind {
	case "abandoned-stale":
		return "stale entry; clear with `git -C <repo> worktree prune`"
	case "abandoned-orphan":
		return "orphan dir; git untracked, remove by hand if unwanted"
	}
	return ""
}

// emitTable writes the human-readable report to out. STATUS is the last column
// so its colour codes never disturb tabwriter alignment.
func emitTable(out io.Writer, root string, wts []Worktree, withSize bool, pal palette) {
	if len(wts) == 0 {
		fmt.Fprintln(out, "No worktrees found under", root)
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	header := "REPO\tWORKTREE\tBRANCH\tBASE"
	if withSize {
		header += "\tSIZE"
	}
	header += "\tSTATUS"
	fmt.Fprintln(tw, header)

	var merged, unmerged, abandoned, errored int
	var reclaimable int64
	for _, w := range wts {
		switch {
		case w.Err != "":
			errored++
		case w.Kind != "live":
			abandoned++
		case w.Merged:
			merged++
			if w.SizeBytes > 0 {
				reclaimable += w.SizeBytes
			}
		default:
			unmerged++
		}
		branch := w.Branch
		if branch == "" {
			branch = "-"
		}
		if w.Detached {
			branch = "(detached)"
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s", repoLabel(root, w.Repo), w.Name, branch, baseLabel(w))
		if withSize {
			row += "\t" + humanSize(w.SizeBytes)
		}
		row += "\t" + paintStatus(pal, w, statusLabel(w))
		fmt.Fprintln(tw, row)
	}
	tw.Flush()

	fmt.Fprintf(out, "\n%d worktree(s) in %d repo(s) - %d merged - %d unmerged - %d abandoned",
		len(wts), countRepos(wts), merged, unmerged, abandoned)
	if errored > 0 {
		fmt.Fprintf(out, " - %d error", errored)
	}
	fmt.Fprintln(out)
	if merged > 0 {
		hint := ""
		if reclaimable > 0 {
			hint = fmt.Sprintf(" (~%s)", humanSize(reclaimable))
		}
		fmt.Fprintf(out, "%d merged worktree(s) removable%s - run with --prune to reap them.\n", merged, hint)
	}
	if abandoned > 0 {
		fmt.Fprintf(out, "%d abandoned worktree(s) - not auto-removed; see STATUS for the fix.\n", abandoned)
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
	Action string `json:"action"` // removed | would-remove | skipped | failed | kept
	Reason string `json:"reason,omitempty"`
	Freed  int64  `json:"freed_bytes,omitempty"`
}

// runPrune removes (or, with dryRun, would remove) every merged live worktree
// and returns a process exit code. Abandoned worktrees are never removed.
func runPrune(root string, wts []Worktree, force, dryRun, jsonOut, noPager bool, pal palette) int {
	var actions []pruneAction
	var freed int64
	var removed, would, skipped, failed, kept int

	for _, w := range wts {
		a := pruneAction{Path: w.Path, Repo: w.Repo, Name: w.Name}
		switch {
		case w.Kind != "live":
			a.Action, a.Reason = "kept", abandonedFix(w)
			kept++
		case !w.Prunable():
			a.Action = "kept"
			if w.Err != "" {
				a.Reason = w.Err
			} else {
				a.Reason = "not merged"
			}
			kept++
		case (w.Dirty || w.Locked) && !force:
			a.Action = "skipped"
			if w.Dirty {
				a.Reason = "uncommitted changes (rerun with --force)"
			} else {
				a.Reason = "locked (rerun with --force)"
			}
			skipped++
		case dryRun:
			a.Action = "would-remove"
			if w.SizeBytes > 0 {
				a.Freed = w.SizeBytes
				freed += w.SizeBytes
			}
			would++
		default:
			if err := removeWorktree(w.Repo, w.Path, w.Dirty || w.Locked); err != nil {
				a.Action, a.Reason = "failed", err.Error()
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
			Root        string        `json:"root"`
			DryRun      bool          `json:"dry_run"`
			Removed     int           `json:"removed"`
			WouldRemove int           `json:"would_remove"`
			Skipped     int           `json:"skipped"`
			Failed      int           `json:"failed"`
			Kept        int           `json:"kept"`
			FreedBytes  int64         `json:"freed_bytes"`
			Actions     []pruneAction `json:"actions"`
		}{root, dryRun, removed, would, skipped, failed, kept, freed, actions})
	} else {
		withPager(noPager, func(out io.Writer) {
			for _, a := range actions {
				if a.Action == "kept" {
					continue // unmerged / abandoned are the normal case; stay quiet
				}
				word := a.Action
				switch a.Action {
				case "removed":
					word = pal.green(word)
				case "failed":
					word = pal.red(word)
				}
				line := fmt.Sprintf("  %-14s %s/%s", word, repoLabel(root, a.Repo), a.Name)
				if a.Reason != "" {
					line += "  (" + a.Reason + ")"
				}
				fmt.Fprintln(out, line)
			}
			if dryRun {
				fmt.Fprintf(out, "\nwould remove %d - skipped %d - kept %d", would, skipped, kept)
			} else {
				fmt.Fprintf(out, "\nremoved %d - skipped %d - failed %d - kept %d", removed, skipped, failed, kept)
			}
			if freed > 0 {
				verb := "reclaimed"
				if dryRun {
					verb = "would reclaim"
				}
				fmt.Fprintf(out, " - %s ~%s", verb, humanSize(freed))
			}
			fmt.Fprintln(out)
			if dryRun {
				fmt.Fprintln(out, "(dry run - nothing was removed; rerun without --dry-run to apply)")
			}
		})
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
