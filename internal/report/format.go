package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/S-Nakamur-a/wherenv/internal/tracer"
)

// Options controls the formatter's output behaviour.
//
// There is deliberately no "show value" option: wherenv never holds variable
// values (they are dropped at capture time), so there is nothing to reveal.
// The formatter only ever prints locations and provenance.
type Options struct {
	JSON  bool // ADR-7: emit JSON instead of the default TSV
	Human bool // emit the human-readable, formatted text instead of the default TSV
	Color bool // colorize human text output (never applies to TSV or JSON)
	// ShowModes is true when >1 mode was traced: tag sites by mode and show
	// per-mode winners. When false (single mode) the human output is a simple
	// load-ordered list with the last/effective assignment marked. It does not
	// affect the TSV/JSON formats, which always carry each site's modes.
	ShowModes bool
}

// Print writes all findings to w. The default format is machine-readable TSV
// (one record per line, tab-separated, no decoration) so the output pipes
// cleanly into grep/awk/cut. Opt into JSON with Options.JSON, or the
// human-readable formatted view with Options.Human.
func Print(w io.Writer, findings []Finding, opts Options) error {
	switch {
	case opts.JSON:
		return printJSON(w, findings)
	case opts.Human:
		return printText(w, findings, opts)
	default:
		return printTSV(w, findings)
	}
}

// ── JSON output ──────────────────────────────────────────────────────────────

// jsonFinding is the stable JSON structure emitted for ADR-7.
//
// There is no value/raw_code field: wherenv never carries values, so the JSON
// reports only locations and provenance.
type jsonFinding struct {
	Name                 string          `json:"name"`
	Origin               string          `json:"origin"`
	Sites                []jsonSite      `json:"sites,omitempty"`
	Verdict              *jsonVerdict    `json:"verdict,omitempty"`
	InheritedFromLaunchd bool            `json:"inherited_from_launchd,omitempty"`
	ToolSource           *jsonToolSource `json:"tool_source,omitempty"`
	SentinelMissing      bool            `json:"sentinel_missing,omitempty"`
}

// jsonToolSource is the JSON representation of a ToolSource (ADR-8).
type jsonToolSource struct {
	Tool string `json:"tool"`
	File string `json:"file,omitempty"`
}

type jsonSite struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Confidence string   `json:"line_confidence"`
	Append     bool     `json:"append,omitempty"`
	Modes      []string `json:"modes"`
	// CallerFile/CallerLine are populated when the assignment ran inside a helper
	// function; they point at the call site (the file the user edits), while
	// File/Line point at the helper that performed the export.
	CallerFile string `json:"caller_file,omitempty"`
	CallerLine int    `json:"caller_line,omitempty"`
}

type jsonVerdict struct {
	PerMode   map[string]jsonSite `json:"per_mode"`
	HasAppend bool                `json:"has_append,omitempty"`
}

func printJSON(w io.Writer, findings []Finding) error {
	out := make([]jsonFinding, 0, len(findings))
	for _, f := range findings {
		jf := jsonFinding{
			Name:                 f.Name,
			Origin:               originString(f.Origin),
			InheritedFromLaunchd: f.InheritedFromLaunchd,
			SentinelMissing:      f.SentinelMissing,
		}
		for _, s := range f.Sites {
			js := siteToJSON(s)
			jf.Sites = append(jf.Sites, js)
		}
		if f.Origin == Toolset && f.ToolSource != nil {
			ts := f.ToolSource
			jf.ToolSource = &jsonToolSource{
				Tool: ts.Tool,
				File: sanitize(ts.File),
			}
		}
		if f.Origin == Startup && len(f.Verdict.PerMode) > 0 {
			jv := &jsonVerdict{
				PerMode:   make(map[string]jsonSite),
				HasAppend: f.Verdict.HasAppend,
			}
			for mode, site := range f.Verdict.PerMode {
				jv.PerMode[modeString(mode)] = siteToJSON(*site)
			}
			jf.Verdict = jv
		}
		out = append(out, jf)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Output is consumed as JSON/CLI text, not embedded in HTML; keep '<', '>',
	// '&' literal instead of \u00XX.
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

func siteToJSON(s AssignmentSite) jsonSite {
	js := jsonSite{
		File:       sanitize(s.File),
		Line:       s.Line,
		Confidence: confidenceString(s.LineConf),
		Append:     s.Append,
	}
	if s.CallerFile != "" {
		js.CallerFile = sanitize(s.CallerFile)
		js.CallerLine = s.CallerLine
	}
	for _, m := range s.Modes {
		js.Modes = append(js.Modes, modeString(m))
	}
	return js
}

// ── TSV output (default, machine-readable) ────────────────────────────────────

// printTSV writes one record per line in a stable, tab-separated layout with no
// decoration — the format meant for pipelines (grep/awk/cut). There is no header
// row, so every line is data. Columns, in order:
//
//	1 name             variable name
//	2 origin           startup | inherited | toolset | unset
//	3 file             source file (empty when none); control chars sanitized
//	4 line             1-based line number (empty when unknown/not applicable)
//	5 line_confidence  exact | best-effort | unknown (empty when not applicable)
//	6 modes            e.g. login, non-login, non-login+login (empty when N/A)
//	7 attrs            comma-separated tokens (empty when none):
//	                     winner=<mode>  this site is the effective last assignment for <mode>
//	                     append         this site used += (cumulative)
//	                     tool=<name>    the tool that set a toolset variable
//	                     launchd        inherited from the macOS launchd session
//	                     incomplete     the startup trace ended before its sentinel
//	8 caller_file      when the assignment ran inside a helper function, the file
//	                     that called it (the line you edit); empty for a direct
//	                     assignment. Columns 3/4 stay the precise mechanism.
//	9 caller_line      1-based line in caller_file (empty when not applicable)
//
// caller_file/caller_line are their own columns rather than an attrs token
// because a file path may contain a comma, which would break attrs parsing.
//
// A Startup variable emits one line per assignment site (so callers can grep by
// file or cut the line number); every other origin emits exactly one line.
func printTSV(w io.Writer, findings []Finding) error {
	for _, f := range findings {
		if err := printTSVFinding(w, f); err != nil {
			return err
		}
	}
	return nil
}

func printTSVFinding(w io.Writer, f Finding) error {
	origin := originString(f.Origin)
	switch f.Origin {
	case Startup:
		if len(f.Sites) == 0 {
			// Defensive: a Startup finding should carry sites, but never drop it.
			return tsvLine(w, f.Name, origin, "", "", "", "", startupBaseAttrs(f, nil), "", "")
		}
		for _, s := range f.Sites {
			file := tsvField(s.File)
			line := ""
			if s.Line > 0 {
				line = strconv.Itoa(s.Line)
			}
			conf := confidenceString(s.LineConf)
			modes := formatModes(s.Modes)
			callerFile, callerLine := tsvCaller(s)
			if err := tsvLine(w, f.Name, origin, file, line, conf, modes, startupBaseAttrs(f, &s), callerFile, callerLine); err != nil {
				return err
			}
		}
		return nil
	case Toolset:
		attrs := []string{}
		file := ""
		if f.ToolSource != nil {
			file = tsvField(f.ToolSource.File)
			attrs = append(attrs, "tool="+f.ToolSource.Tool)
		}
		if f.SentinelMissing {
			attrs = append(attrs, "incomplete")
		}
		return tsvLine(w, f.Name, origin, file, "", "", "", attrs, "", "")
	case Inherited:
		attrs := []string{}
		if f.InheritedFromLaunchd {
			attrs = append(attrs, "launchd")
		}
		if f.SentinelMissing {
			attrs = append(attrs, "incomplete")
		}
		return tsvLine(w, f.Name, origin, "", "", "", "", attrs, "", "")
	default: // Unset
		return tsvLine(w, f.Name, origin, "", "", "", "", nil, "", "")
	}
}

// tsvCaller returns the caller_file/caller_line column values for a site,
// empty when the assignment ran directly (no helper indirection).
func tsvCaller(s AssignmentSite) (callerFile, callerLine string) {
	if s.CallerFile == "" {
		return "", ""
	}
	cl := ""
	if s.CallerLine > 0 {
		cl = strconv.Itoa(s.CallerLine)
	}
	return tsvField(s.CallerFile), cl
}

// startupBaseAttrs builds the attrs tokens for a Startup site: any winner=<mode>
// tags (the site is the effective last assignment for that mode), an append tag,
// and an incomplete tag when the trace was cut short. site may be nil.
func startupBaseAttrs(f Finding, site *AssignmentSite) []string {
	var attrs []string
	if site != nil {
		for _, mode := range []tracer.Mode{tracer.NonLogin, tracer.Login} {
			win, ok := f.Verdict.PerMode[mode]
			if ok && win.File == site.File && win.Line == site.Line {
				attrs = append(attrs, "winner="+modeString(mode))
			}
		}
		if site.Append {
			attrs = append(attrs, "append")
		}
	}
	if f.SentinelMissing {
		attrs = append(attrs, "incomplete")
	}
	return attrs
}

// tsvLine writes one tab-separated record terminated by '\n'. attrs are joined
// with commas into column 7; caller_file/caller_line are columns 8/9.
func tsvLine(w io.Writer, name, origin, file, line, conf, modes string, attrs []string, callerFile, callerLine string) error {
	_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		name, origin, file, line, conf, modes, strings.Join(attrs, ","), callerFile, callerLine)
	return err
}

// tsvField sanitizes a value for use as a TSV column: it first strips terminal
// control characters (S7) and then neutralizes any literal tab so it cannot
// split the record into spurious columns.
func tsvField(s string) string {
	return strings.ReplaceAll(sanitize(s), "\t", " ")
}

// ── Text output ───────────────────────────────────────────────────────────────

func printText(w io.Writer, findings []Finding, opts Options) error {
	for i, f := range findings {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := printOneFinding(w, f, opts); err != nil {
			return err
		}
	}
	return nil
}

func printOneFinding(w io.Writer, f Finding, opts Options) error {
	switch f.Origin {
	case Startup:
		return printStartup(w, f, opts)
	case Inherited:
		return printInherited(w, f, opts)
	case Toolset:
		return printToolset(w, f, opts)
	case Unset:
		pal := palette{on: opts.Color}
		fmt.Fprintf(w, "%s: %s\n", pal.name(f.Name), pal.bad("not set"))
		return nil
	}
	return nil
}

func printStartup(w io.Writer, f Finding, opts Options) error {
	pal := palette{on: opts.Color}
	if opts.ShowModes {
		return printStartupByMode(w, f, pal)
	}
	return printStartupStack(w, f, pal)
}

// printStartupStack renders the single-mode view like a stack trace: the most
// recent (effective) assignment first, older overridden ones below it.
func printStartupStack(w io.Writer, f Finding, pal palette) error {
	n := len(f.Sites)
	suffix := ""
	if n > 1 {
		suffix = fmt.Sprintf("  (%d places, most recent first)", n)
	}
	fmt.Fprintf(w, "%s: set by startup%s\n", pal.name(f.Name), pal.dim(suffix))
	if f.SentinelMissing {
		fmt.Fprintln(w, pal.warn("  (warning: startup trace may be incomplete — shell exited before sentinel)"))
	}

	// We mark only the LAST assignment in execution order — a plain fact about
	// ordering. We deliberately do NOT claim which assignment "wins": with zsh
	// arrays (path=(... $path)), parameter expansion and value transforms,
	// deciding override-vs-cumulative reliably would mean replaying the value
	// itself. The honest output is "here are the places, newest first" — use -v
	// to see the values and judge precedence yourself.
	var winFile string
	var winLine int
	haveWin := false
	for _, s := range f.Verdict.PerMode {
		winFile, winLine, haveWin = s.File, s.Line, true
		break
	}

	if n > 1 {
		fmt.Fprintln(w)
	}
	// Most-recent first: reverse of execution order.
	for i := n - 1; i >= 0; i-- {
		s := f.Sites[i]
		loc, via := siteLoc(s, pal)
		conf := formatConfidence(s.LineConf, s.ShellVersion)
		last := n > 1 && haveWin && s.File == winFile && s.Line == winLine

		switch {
		case n == 1:
			fmt.Fprintf(w, "  %s%s%s\n", pal.loc(loc), pal.dim(conf), via)
		case last:
			fmt.Fprintf(w, "  %s %s%s%s   %s\n", pal.dim("→"), pal.winner(loc), pal.dim(conf), via, pal.dim("← ran last"))
		default:
			fmt.Fprintf(w, "    %s%s%s\n", pal.loc(loc), pal.dim(conf), via)
		}
	}
	return nil
}

// siteLoc returns the primary location string to display for a site and an
// optional " (via …)" suffix. When the site carries a caller location (the
// assignment ran inside a helper function), the caller line — the file the user
// actually edits — becomes primary and the mechanism (the helper's own
// file:line) is demoted to the via note. Without a caller, the site's own
// file:line is shown as before.
func siteLoc(s AssignmentSite, pal palette) (loc, via string) {
	if s.CallerFile != "" {
		loc = fmt.Sprintf("%s:%d", sanitize(s.CallerFile), s.CallerLine)
		via = "  " + pal.dim("(via "+formatFileLine(s)+")")
		return loc, via
	}
	return formatFileLine(s), ""
}

// printStartupByMode renders the --mode both view: each site tagged with the
// mode(s) it appeared in, plus per-mode winners (the answer differs by mode).
func printStartupByMode(w io.Writer, f Finding, pal palette) error {
	fmt.Fprintf(w, "%s: set by startup\n", pal.name(f.Name))
	if f.SentinelMissing {
		fmt.Fprintln(w, pal.warn("  (warning: startup trace may be incomplete — shell exited before sentinel)"))
	}

	for _, s := range f.Sites {
		loc, via := siteLoc(s, pal)
		modes := formatModes(s.Modes)
		conf := formatConfidence(s.LineConf, s.ShellVersion)
		fmt.Fprintf(w, "  %s %s%s%s\n", pal.loc(loc), pal.dim("["+modes+"]"), pal.dim(conf), via)
		if s.Append {
			fmt.Fprintln(w, pal.dim("    (appends with +=)"))
		}
	}

	if len(f.Verdict.PerMode) > 0 {
		fmt.Fprintln(w, "  winner:")
		for _, mode := range []tracer.Mode{tracer.NonLogin, tracer.Login} {
			site, ok := f.Verdict.PerMode[mode]
			if !ok {
				continue
			}
			loc, via := siteLoc(*site, pal)
			conf := formatConfidence(site.LineConf, site.ShellVersion)
			appendNote := ""
			if site.Append {
				appendNote = " (+=)"
			}
			// Tab after the mode tag so file:line aligns across [login]/[non-login].
			fmt.Fprintf(w, "    [%s]\t%s%s%s%s\n", modeString(mode), pal.winner(loc), pal.dim(conf), appendNote, via)
		}
		if f.Verdict.HasAppend {
			fmt.Fprintln(w, pal.dim("    (chain includes += assignments)"))
		}
	}
	return nil
}

func printInherited(w io.Writer, f Finding, opts Options) error {
	pal := palette{on: opts.Color}
	fmt.Fprintf(w, "%s: %s\n", pal.name(f.Name),
		pal.warn("present in the environment, not set by any startup file"))

	if f.SentinelMissing {
		// The trace was cut short, so we cannot conclude the variable is external —
		// it may in fact be set later in a startup file we couldn't finish tracing.
		fmt.Fprintln(w, pal.warn("  → but the startup trace was INCOMPLETE; it may actually be set in startup. Retry (e.g. --timeout 20)."))
		return nil
	}

	if f.InheritedFromLaunchd {
		fmt.Fprintf(w, "  %s\n", pal.dim("→ set in the launchd session (via launchctl)"))
	} else {
		fmt.Fprintln(w, pal.dim("  → inherited from the parent process, or exported interactively / by a tool (these can't be traced)"))
	}
	return nil
}

func printToolset(w io.Writer, f Finding, opts Options) error {
	pal := palette{on: opts.Color}

	if f.ToolSource == nil {
		// Should not happen in practice; degrade gracefully with a generic header.
		fmt.Fprintf(w, "%s: set by tool\n", pal.name(f.Name))
		fmt.Fprintln(w, pal.dim("  → source path unavailable"))
		return nil
	}

	// Header: always uses ToolSource.Tool (tool-agnostic, not hardcoded).
	fmt.Fprintf(w, "%s: set by %s\n", pal.name(f.Name), f.ToolSource.Tool)

	if f.ToolSource.File != "" {
		fmt.Fprintf(w, "  %s\n", pal.loc("→ from "+sanitize(f.ToolSource.File)))
	} else {
		fmt.Fprintf(w, "  %s\n", pal.dim("→ set by "+f.ToolSource.Tool+" (source path unavailable)"))
	}

	fmt.Fprintln(w, pal.dim("  ("+toolScopeNote(f.ToolSource.Tool)+")"))
	return nil
}

// toolScopeNote returns the parenthetical scope note appended below the
// source-file line. Each tool gets a tailored description; unknown tools
// fall back to a generic note.
func toolScopeNote(tool string) string {
	switch tool {
	case "direnv":
		return "direnv loads this in the current directory, after your shell startup — directory-scoped"
	case "mise":
		return "mise loads this from the nearest mise.toml, after your shell startup — directory-scoped"
	default:
		return tool + " sets this after your shell startup — directory-scoped"
	}
}

// ── Formatting helpers ────────────────────────────────────────────────────────

// formatFileLine formats a site's file:line with appropriate caveats for
// bash 3.2 truncation (LineUnknown) or approximate line numbers (LineBestEffort).
func formatFileLine(s AssignmentSite) string {
	file := sanitize(s.File)
	switch s.LineConf {
	case tracer.LineExact:
		if s.Line > 0 {
			return fmt.Sprintf("%s:%d", file, s.Line)
		}
		return file
	case tracer.LineBestEffort:
		if s.Line > 0 {
			return fmt.Sprintf("%s:%d", file, s.Line)
		}
		return file
	case tracer.LineUnknown:
		// bash 3.2 truncation: file may be cut short; line not available.
		if s.Line > 0 {
			return fmt.Sprintf("%s:%d", file, s.Line)
		}
		return fmt.Sprintf("%s…", file)
	}
	return file
}

// formatConfidence returns a parenthetical annotation for a site's line confidence.
// shellVersion is included in the LineUnknown message when non-empty, so that
// format.go does not need to know the shell dialect (ADR-5).
func formatConfidence(lc tracer.LineConfidence, shellVersion string) string {
	switch lc {
	case tracer.LineBestEffort:
		return " (line number is approximate)"
	case tracer.LineUnknown:
		if shellVersion != "" {
			return fmt.Sprintf(" (PS4 truncation [%s] — source location uncertain)", shellVersion)
		}
		return " (PS4 truncation — source location uncertain)"
	}
	return ""
}

func formatModes(modes []tracer.Mode) string {
	parts := make([]string, 0, len(modes))
	for _, m := range modes {
		parts = append(parts, modeString(m))
	}
	return strings.Join(parts, "+")
}

// ── String converters ─────────────────────────────────────────────────────────

func originString(o Origin) string {
	switch o {
	case Startup:
		return "startup"
	case Inherited:
		return "inherited"
	case Unset:
		return "unset"
	case Toolset:
		return "toolset"
	}
	return "unknown"
}

func modeString(m tracer.Mode) string {
	switch m {
	case tracer.NonLogin:
		return "non-login"
	case tracer.Login:
		return "login"
	}
	return fmt.Sprintf("mode(%d)", m)
}

func confidenceString(lc tracer.LineConfidence) string {
	switch lc {
	case tracer.LineExact:
		return "exact"
	case tracer.LineBestEffort:
		return "best-effort"
	case tracer.LineUnknown:
		return "unknown"
	}
	return "?"
}

// sanitize removes or replaces terminal-control characters from s (S7).
// Printable chars, space, and tab pass through; everything else becomes '?'.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == ' ' {
			b.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			// Replace ESC, carriage return, and all other C0/C1 controls.
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
