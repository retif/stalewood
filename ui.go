package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// isTTY reports whether f is connected to a terminal. It uses the stdlib
// character-device check — sufficient for colour/progress/pager decisions; the
// only false positive (a non-tty char device such as /dev/null) is harmless.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// reporter writes progress and verbose diagnostics to stderr, keeping stdout
// reserved for the report. A nil *reporter is a valid no-op (used in tests).
type reporter struct {
	w       io.Writer
	tty     bool
	verbose bool
	quiet   bool
}

func newReporter(verbose, quiet bool) *reporter {
	return &reporter{w: os.Stderr, tty: isTTY(os.Stderr), verbose: verbose, quiet: quiet}
}

// progress shows a transient, overwritable status line. It is shown only on an
// interactive stderr in non-verbose mode; otherwise it is a no-op.
func (r *reporter) progress(format string, a ...any) {
	if r == nil || r.quiet || r.verbose || !r.tty {
		return
	}
	fmt.Fprintf(r.w, "\r\033[2K%s", fmt.Sprintf(format, a...))
}

// clear erases the current transient progress line.
func (r *reporter) clear() {
	if r == nil || r.quiet || r.verbose || !r.tty {
		return
	}
	fmt.Fprint(r.w, "\r\033[2K")
}

// note prints a durable line to stderr — only in verbose mode.
func (r *reporter) note(format string, a ...any) {
	if r == nil || r.quiet || !r.verbose {
		return
	}
	fmt.Fprintf(r.w, format+"\n", a...)
}

// palette renders ANSI colour, enabled only for an interactive stdout with
// NO_COLOR unset (https://no-color.org).
type palette struct{ enabled bool }

func newPalette() palette {
	_, noColor := os.LookupEnv("NO_COLOR")
	return palette{enabled: isTTY(os.Stdout) && !noColor}
}

func (p palette) paint(code, s string) string {
	if !p.enabled || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (p palette) green(s string) string  { return p.paint("32", s) }
func (p palette) yellow(s string) string { return p.paint("33", s) }
func (p palette) red(s string) string    { return p.paint("31", s) }

// withPager runs fn with a writer that is the system pager's stdin when stdout
// is an interactive terminal and paging is not disabled, or stdout directly
// otherwise. less is invoked with -FIRX, so output that fits one screen is
// printed without trapping the user in the pager.
func withPager(disabled bool, fn func(io.Writer)) {
	if disabled || !isTTY(os.Stdout) {
		fn(os.Stdout)
		return
	}
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}
	parts := strings.Fields(pager)
	if len(parts) == 0 {
		fn(os.Stdout)
		return
	}
	args := parts[1:]
	if filepath.Base(parts[0]) == "less" && len(args) == 0 {
		args = []string{"-FIRX"}
	}
	cmd := exec.Command(parts[0], args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		fn(os.Stdout)
		return
	}
	if err := cmd.Start(); err != nil {
		fn(os.Stdout)
		return
	}
	fn(in)
	in.Close()
	cmd.Wait()
}
