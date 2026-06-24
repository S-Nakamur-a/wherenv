package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/S-Nakamur-a/wherenv/internal/classify"
	"github.com/S-Nakamur-a/wherenv/internal/direnv"
	"github.com/S-Nakamur-a/wherenv/internal/env"
	"github.com/S-Nakamur-a/wherenv/internal/inherit"
	"github.com/S-Nakamur-a/wherenv/internal/mise"
	"github.com/S-Nakamur-a/wherenv/internal/report"
	"github.com/S-Nakamur-a/wherenv/internal/tracer"
)

// validKey is the regex for S1: valid environment variable names.
var validKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is the testable entry point. args are the CLI arguments (without argv[0]),
// getenv is used for SHELL lookup, stdout/stderr receive output.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	// ── Flags ──────────────────────────────────────────────────────────────────
	fs := flag.NewFlagSet("wherenv", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON output")
	// Default output is machine-readable TSV (pipeline-friendly). --human/-H
	// switches to the formatted, human-readable view. Note: -h is reserved by the
	// flag package for help, so the short alias is -H.
	var human bool
	fs.BoolVar(&human, "human", false, "human-readable, formatted output (default is machine-readable TSV)")
	fs.BoolVar(&human, "H", false, "alias for --human")
	timeoutSec := fs.Float64("timeout", 8.0, "per-spawn timeout in seconds")
	modeFlag := fs.String("mode", "login", "shell mode(s) to trace: login | non-login | both")
	colorFlag := fs.String("color", "auto", "colorize output: auto | always | never (human output only)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: wherenv [flags] VARNAME [VARNAME...]")
		fmt.Fprintln(stderr, "wherenv reports WHERE each variable was set; it never reads or prints values.")
		fmt.Fprintln(stderr, "Default output is machine-readable TSV; use --human/-H for the formatted view.")
		fmt.Fprintln(stderr, "WARNING: wherenv executes your real shell startup files as a side effect of tracing.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		// ContinueOnError: fs.Parse writes the error to stderr; we return 2.
		return 2
	}
	keys := fs.Args()

	if len(keys) == 0 {
		fs.Usage()
		return 2
	}

	// ── Validate --timeout ────────────────────────────────────────────────────
	if *timeoutSec <= 0 {
		fmt.Fprintln(stderr, "wherenv: --timeout must be > 0")
		return 2
	}

	// ── Resolve --mode into the list of modes to trace ─────────────────────────
	// Default is login: on zsh a login-interactive shell is a SUPERSET of
	// non-login (it also sources .zshrc), and matches how macOS terminals start.
	var modes []tracer.Mode
	switch *modeFlag {
	case "login":
		modes = []tracer.Mode{tracer.Login}
	case "non-login":
		modes = []tracer.Mode{tracer.NonLogin}
	case "both":
		modes = []tracer.Mode{tracer.NonLogin, tracer.Login}
	default:
		fmt.Fprintf(stderr, "wherenv: invalid --mode %q (want login | non-login | both)\n", *modeFlag)
		return 2
	}

	// ── Resolve --color ────────────────────────────────────────────────────────
	var colorize bool
	switch *colorFlag {
	case "always":
		colorize = true
	case "never":
		colorize = false
	case "auto":
		colorize = isTerminalWriter(stdout) && getenv("NO_COLOR") == ""
	default:
		fmt.Fprintf(stderr, "wherenv: invalid --color %q (want auto | always | never)\n", *colorFlag)
		return 2
	}

	// ── S1: validate all keys before doing anything else ──────────────────────
	for _, key := range keys {
		if !validKey.MatchString(key) {
			fmt.Fprintf(stderr, "wherenv: invalid variable name %q (must match ^[A-Za-z_][A-Za-z0-9_]*$)\n", key)
			return 2
		}
	}

	// ── Env snapshot ──────────────────────────────────────────────────────────
	snap := env.Snapshot()

	// keysSet used by tracers (S2: never interpolated into shell argv).
	keysSet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keysSet[k] = struct{}{}
	}

	timeout := time.Duration(*timeoutSec * float64(time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), timeout*3) // outer budget = 3× per-spawn
	defer cancel()

	// ── Select tracer based on $SHELL ─────────────────────────────────────────
	shellBin := filepath.Base(getenv("SHELL"))
	var tr tracer.Tracer
	switch shellBin {
	case "zsh":
		tr = tracer.ZshTracer()
	case "bash":
		tr = tracer.BashTracer()
	}

	var results []tracer.TraceResult

	if tr != nil && tr.Available() {
		// Progress feedback: tracing spawns a real shell and can take a moment.
		// Show an animated spinner that names the current step — but only on a
		// TTY (keeps pipes/JSON consumers clean). Warnings are collected and
		// printed after the spinner is cleared so they don't garble the line.
		var sp *spinner
		if isTerminalWriter(stderr) {
			sp = startSpinner(stderr, "tracing "+shellBin+" startup…")
		}
		var warnings []string
		// Run the selected mode(s).
		for _, mode := range modes {
			if sp != nil {
				sp.setLabel(fmt.Sprintf("tracing %s startup (%s)…", shellBin, modeLabel(mode)))
			}
			r, err := tr.Trace(ctx, mode, keysSet, timeout)
			if err != nil {
				// Non-fatal: timeout or partial trace; use whatever we got.
				warnings = append(warnings, fmt.Sprintf("wherenv: %s %s trace warning: %v", tr.Name(), modeLabel(mode), err))
			}
			results = append(results, r)
		}
		if sp != nil {
			sp.stopAndClear()
		}
		for _, wmsg := range warnings {
			fmt.Fprintln(stderr, wmsg)
		}
	} else {
		// Unsupported or unavailable shell: degrade gracefully.
		shell := getenv("SHELL")
		fmt.Fprintf(stderr, "wherenv: unsupported shell %q — startup tracing skipped; classifying from env only\n", shell)
	}

	// ── Classify ──────────────────────────────────────────────────────────────
	findings := classify.Classify(results, snap, keys)

	// ── Tool probe: direnv → mise → Inherited fallback ───────────────────────
	// S1: all names were already validated above.
	// mise.Probe is called once here (outside any per-var loop) to build the
	// full map of mise-managed variables. The map is then passed to
	// elevateOrigins which processes each variable in order.
	miseSources := mise.Probe(mise.DefaultRunner)
	elevateOrigins(findings, snap, miseSources, inherit.Probe)

	// ── Report ────────────────────────────────────────────────────────────────
	opts := report.Options{
		JSON:  *asJSON,
		Human: human,
		// Color only ever applies to the human view; the TSV/JSON formats are
		// always plain. Gate it on human so --color=always can't leak ANSI into a
		// machine-readable stream.
		Color:     colorize && human,
		ShowModes: len(modes) > 1,
	}
	if err := report.Print(stdout, findings, opts); err != nil {
		fmt.Fprintln(stderr, "wherenv:", err)
		return 1
	}
	return 0
}

// elevateOrigins iterates over findings and, for each Inherited variable,
// checks in order: (1) direnv probe — if it matches, the finding is promoted
// to Toolset; (2) mise probe via miseSources map — if a match is found, also
// elevated to Toolset; (3) launchctl probe via launchctlProbe — sets
// InheritedFromLaunchd for macOS session-level variables. Only Inherited
// findings are touched; Startup/Unset/Toolset pass through unchanged.
//
// miseSources is the pre-built map from mise.Probe (called once outside this
// function to avoid per-variable exec calls). launchctlProbe is injected so
// callers in tests can supply a stub without spawning exec.Command (S5); it
// returns only a presence bit, never the variable's value.
func elevateOrigins(findings []report.Finding, snap map[string]string, miseSources map[string]report.ToolSource, launchctlProbe func(string) bool) {
	for i := range findings {
		if findings[i].Origin != report.Inherited {
			continue
		}
		// 1. direnv: DIRENV_DIFF-based probe — most specific, checked first.
		if src, ok := direnv.Probe(snap, findings[i].Name); ok {
			findings[i].Origin = report.Toolset
			findings[i].ToolSource = &src
			continue
		}
		// 2. mise: pre-built map from `mise env --json-extended`.
		if src, ok := miseSources[findings[i].Name]; ok {
			findings[i].Origin = report.Toolset
			findings[i].ToolSource = &src
			continue
		}
		// 3. launchctl: macOS session-level env store (S5: no shell interpolation).
		findings[i].InheritedFromLaunchd = launchctlProbe(findings[i].Name)
	}
}

// isTerminalWriter reports whether w is a character device (a TTY). Used to
// gate interactive progress output so pipes and JSON consumers stay clean.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func modeLabel(m tracer.Mode) string {
	if m == tracer.Login {
		return "login"
	}
	return "non-login"
}
