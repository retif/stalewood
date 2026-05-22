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
	"sort"
	"strings"
)

const version = "0.1.0"

func main() {
	fs := flag.NewFlagSet("stalewood", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // usage and errors are printed by this file
	fs.Usage = func() {}

	prune := fs.Bool("prune", false, "remove worktrees whose work is merged")
	dryRun := fs.Bool("dry-run", false, "with --prune, show what would be removed without removing it")
	force := fs.Bool("force", false, "with --prune, also remove merged worktrees that are dirty or locked")
	jsonOut := fs.Bool("json", false, "emit JSON instead of the tree")
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
		withPager(*noPager, func(w io.Writer) { emitTree(w, abs, wts, *withSize, pal) })
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

The report is a tree grouped by repo: each worktree shows a glyph (merged,
unmerged, abandoned, error), its full path, branch and base, plus tags such as
[claude], [modified files], [untracked files], [lock-stale]. A legend prints below it.

Flags:
  --prune        remove worktrees whose work is merged
  --dry-run      with --prune, show what would be removed without removing it
  --force        with --prune, also remove merged worktrees that are dirty/locked
  --size         measure each worktree's disk usage
  --base REF     test every worktree against REF instead of its own base
  --json         emit JSON instead of the tree
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

// verdictText is the merge verdict word(s) for a worktree.
func verdictText(w Worktree) string {
	if w.Err != "" {
		return "error"
	}
	switch w.Kind {
	case "abandoned-orphan":
		return "abandoned (orphan dir)"
	case "abandoned-stale":
		return "abandoned (stale entry)"
	default:
		return w.Status()
	}
}

// glyph is the single-character status marker for a worktree.
func glyph(w Worktree) string {
	switch {
	case w.Err != "":
		return "!"
	case w.Kind != "live":
		return "⚠" // warning sign
	case w.Merged:
		return "✓" // check mark
	default:
		return "✗" // ballot x
	}
}

// severityCode is the SGR code (bold + colour) for a worktree's verdict.
func severityCode(w Worktree) string {
	switch {
	case w.Err != "":
		return "1;31" // bold red
	case w.Kind != "live":
		return "1;33" // bold yellow
	case w.Merged:
		return "1;32" // bold green
	default:
		return "1" // bold, default colour
	}
}

func paintSeverity(pal palette, w Worktree, s string) string {
	return pal.paint(severityCode(w), s)
}

// worktreeTags returns the bracketed indicators for a worktree.
func worktreeTags(w Worktree) []string {
	var tags []string
	if w.Claude {
		tags = append(tags, "claude")
	}
	if w.Modified {
		tags = append(tags, "modified files")
	}
	if w.Untracked {
		tags = append(tags, "untracked files")
	}
	switch {
	case w.LockStale():
		tags = append(tags, "lock-stale")
	case w.Locked:
		tags = append(tags, "locked")
	}
	if w.GitPrunable {
		tags = append(tags, "git-prunable")
	}
	return tags
}

// paintTag colours one tag: yellow for attention, cyan for provenance, dim otherwise.
func paintTag(pal palette, tag string) string {
	t := "[" + tag + "]"
	switch tag {
	case "modified files", "untracked files", "lock-stale", "git-prunable":
		return pal.yellow(t)
	case "claude":
		return pal.cyan(t)
	default: // locked
		return pal.dim(t)
	}
}

// statusLabel renders a worktree's plain (uncoloured) status — verdict, an
// optional "-> ref", and bracketed tags. Used for --verbose log lines.
func statusLabel(w Worktree) string {
	if w.Err != "" {
		return "error: " + w.Err
	}
	s := verdictText(w)
	if w.Merged && w.MergedInto != "" && w.MergedInto != w.Base {
		s += " -> " + w.MergedInto
	}
	for _, t := range worktreeTags(w) {
		s += " [" + t + "]"
	}
	return s
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

type wtField struct{ label, value string }

// worktreeFields are the leaf lines printed under a worktree node.
func worktreeFields(w Worktree, withSize bool) []wtField {
	f := []wtField{{"path", w.Path}}
	switch {
	case w.Err != "":
		f = append(f, wtField{"error", w.Err})
	case w.Kind != "live":
		if fix := abandonedFix(w); fix != "" {
			f = append(f, wtField{"fix", fix})
		}
	default:
		branch := w.Branch
		if w.Detached {
			branch = "(detached)"
		}
		if branch == "" {
			branch = "-"
		}
		f = append(f, wtField{"branch", branch}, wtField{"base", baseLabel(w)})
	}
	if withSize {
		f = append(f, wtField{"size", humanSize(w.SizeBytes)})
	}
	return f
}

// emitWorktreeNode prints one worktree as a tree node with its field leaves.
func emitWorktreeNode(out io.Writer, w Worktree, last, withSize bool, pal palette) {
	conn, cont := "├─", "│  " // "├─", "│  "
	if last {
		conn, cont = "└─", "   " // "└─"
	}
	verdict := verdictText(w)
	if w.Merged && w.MergedInto != "" && w.MergedInto != w.Base {
		verdict += " -> " + w.MergedInto
	}
	line := fmt.Sprintf("  %s %s %s  %s",
		pal.dim(conn), paintSeverity(pal, w, glyph(w)),
		pal.bold(w.Name), paintSeverity(pal, w, verdict))
	for _, t := range worktreeTags(w) {
		line += " " + paintTag(pal, t)
	}
	fmt.Fprintln(out, line)

	fields := worktreeFields(w, withSize)
	for i, f := range fields {
		fc := "├──" // "├──"
		if i == len(fields)-1 {
			fc = "└──" // "└──"
		}
		fmt.Fprintf(out, "  %s%s %s%s\n",
			pal.dim(cont), pal.dim(fc), pal.dim(fmt.Sprintf("%-8s", f.label)), f.value)
	}
}

// emitTree writes the human-readable report: a tree grouped by repo, then a
// summary and a present-only legend.
func emitTree(out io.Writer, root string, wts []Worktree, withSize bool, pal palette) {
	if len(wts) == 0 {
		fmt.Fprintln(out, "No worktrees found under", root)
		return
	}
	byRepo := map[string][]Worktree{}
	var repoOrder []string
	for _, w := range wts {
		if _, ok := byRepo[w.Repo]; !ok {
			repoOrder = append(repoOrder, w.Repo)
		}
		byRepo[w.Repo] = append(byRepo[w.Repo], w)
	}
	sort.Strings(repoOrder)

	var merged, unmerged, abandoned, errored int
	var reclaimable int64
	for ri, repo := range repoOrder {
		group := byRepo[repo]
		sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		fmt.Fprintf(out, "%s %s   %s\n",
			pal.boldCyan("●"), pal.bold(repoLabel(root, repo)), pal.dim(repo))
		for wi, w := range group {
			emitWorktreeNode(out, w, wi == len(group)-1, withSize, pal)
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
		}
		if ri < len(repoOrder)-1 {
			fmt.Fprintln(out)
		}
	}

	fmt.Fprintf(out, "\n%d worktree(s) in %d repo(s) - %d merged - %d unmerged - %d abandoned",
		len(wts), len(repoOrder), merged, unmerged, abandoned)
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
		fmt.Fprintf(out, "%d abandoned worktree(s) - not auto-removed; see the fix on each.\n", abandoned)
	}
	printLegend(out, wts, pal)
}

// printLegend describes the glyphs and tags that actually appear in the report.
func printLegend(out io.Writer, wts []Worktree, pal palette) {
	tagSeen := map[string]bool{}
	var anyMerged, anyUnmerged, anyAbandoned, anyError bool
	for _, w := range wts {
		for _, t := range worktreeTags(w) {
			tagSeen[t] = true
		}
		switch {
		case w.Err != "":
			anyError = true
		case w.Kind != "live":
			anyAbandoned = true
		case w.Merged:
			anyMerged = true
		default:
			anyUnmerged = true
		}
	}
	type row struct{ plain, colored, desc string }
	var rows []row
	add := func(show bool, plain, colored, desc string) {
		if show {
			rows = append(rows, row{plain, colored, desc})
		}
	}
	add(anyMerged, "✓", pal.paint("1;32", "✓"), "merged - work is integrated")
	add(anyUnmerged, "✗", pal.bold("✗"), "unmerged - not yet integrated")
	add(anyAbandoned, "⚠", pal.paint("1;33", "⚠"), "abandoned - orphan dir or stale git entry")
	add(anyError, "!", pal.paint("1;31", "!"), "error - could not be analyzed")
	for _, td := range []struct{ tag, desc string }{
		{"claude", "created by Claude Code (.claude/worktrees/)"},
		{"modified files", "tracked files have uncommitted edits"},
		{"untracked files", "files present that git is not tracking"},
		{"locked", "a git worktree lock is held"},
		{"lock-stale", "locked, but the locking process is gone"},
		{"git-prunable", "git's own worktree list flags it prunable"},
	} {
		add(tagSeen[td.tag], "["+td.tag+"]", paintTag(pal, td.tag), td.desc)
	}
	if len(rows) == 0 {
		return
	}
	maxw := 0
	for _, r := range rows {
		if n := len([]rune(r.plain)); n > maxw {
			maxw = n
		}
	}
	fmt.Fprintln(out)
	for _, r := range rows {
		pad := strings.Repeat(" ", maxw-len([]rune(r.plain))+2)
		fmt.Fprintf(out, "  %s%s%s\n", r.colored, pad, r.desc)
	}
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
			switch {
			case w.Dirty:
				a.Reason = "uncommitted changes (rerun with --force)"
			case w.LockStale():
				a.Reason = "stale lock, dead owner - safe to rerun with --force"
			default:
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
