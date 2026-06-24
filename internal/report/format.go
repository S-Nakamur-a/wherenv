package report

import (
	"encoding/json"
	"fmt"
	"io"
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
	JSON      bool // ADR-7: emit JSON instead of text
	Color     bool // colorize text output (never applies to JSON)
	ShowModes bool // true when >1 mode was traced: tag sites by mode and show
	// per-mode winners. When false (single mode) the output is a simple
	// load-ordered list with the last/effective assignment marked.
}

// Print writes all findings to w in text or JSON format.
func Print(w io.Writer, findings []Finding, opts Options) error {
	if opts.JSON {
		return printJSON(w, findings)
	}
	return printText(w, findings, opts)
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
	for _, m := range s.Modes {
		js.Modes = append(js.Modes, modeString(m))
	}
	return js
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
		fileLine := formatFileLine(s)
		conf := formatConfidence(s.LineConf, s.ShellVersion)
		last := n > 1 && haveWin && s.File == winFile && s.Line == winLine

		switch {
		case n == 1:
			fmt.Fprintf(w, "  %s%s\n", pal.loc(fileLine), pal.dim(conf))
		case last:
			fmt.Fprintf(w, "  %s %s%s   %s\n", pal.dim("→"), pal.winner(fileLine), pal.dim(conf), pal.dim("← ran last"))
		default:
			fmt.Fprintf(w, "    %s%s\n", pal.loc(fileLine), pal.dim(conf))
		}
	}
	return nil
}

// printStartupByMode renders the --mode both view: each site tagged with the
// mode(s) it appeared in, plus per-mode winners (the answer differs by mode).
func printStartupByMode(w io.Writer, f Finding, pal palette) error {
	fmt.Fprintf(w, "%s: set by startup\n", pal.name(f.Name))
	if f.SentinelMissing {
		fmt.Fprintln(w, pal.warn("  (warning: startup trace may be incomplete — shell exited before sentinel)"))
	}

	for _, s := range f.Sites {
		fileLine := formatFileLine(s)
		modes := formatModes(s.Modes)
		conf := formatConfidence(s.LineConf, s.ShellVersion)
		fmt.Fprintf(w, "  %s %s%s\n", pal.loc(fileLine), pal.dim("["+modes+"]"), pal.dim(conf))
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
			fileLine := formatFileLine(*site)
			conf := formatConfidence(site.LineConf, site.ShellVersion)
			appendNote := ""
			if site.Append {
				appendNote = " (+=)"
			}
			// Tab after the mode tag so file:line aligns across [login]/[non-login].
			fmt.Fprintf(w, "    [%s]\t%s%s%s\n", modeString(mode), pal.winner(fileLine), pal.dim(conf), appendNote)
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
