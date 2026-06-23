package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/S-Nakamur-a/wherenv/internal/tracer"
)

// maxRawCodeDefault is the default display limit for RawCode values when values
// are revealed (--show-value). Longer lines are truncated with "…".
const maxRawCodeDefault = 120

// valueHidden replaces the right-hand side of an assignment when values are not
// revealed. Values are hidden by DEFAULT so secrets (tokens, keys) are never
// printed unless explicitly requested.
const valueHidden = "<hidden>"

// Options controls the formatter's output behaviour.
type Options struct {
	JSON      bool // ADR-7: emit JSON instead of text
	ShowValue bool // reveal variable values (truncated); default false → values redacted
	FullValue bool // reveal full, untruncated values (implies ShowValue)
	Color     bool // colorize text output (never applies to JSON)
	ShowModes bool // true when >1 mode was traced: tag sites by mode and show
	// per-mode winners. When false (single mode) the output is a simple
	// load-ordered list with the last/effective assignment marked.
}

// Print writes all findings to w in text or JSON format.
func Print(w io.Writer, findings []Finding, opts Options) error {
	if opts.JSON {
		return printJSON(w, findings, opts)
	}
	return printText(w, findings, opts)
}

// ── JSON output ──────────────────────────────────────────────────────────────

// jsonFinding is the stable JSON structure emitted for ADR-7.
type jsonFinding struct {
	Name            string         `json:"name"`
	Origin          string         `json:"origin"`
	Sites           []jsonSite     `json:"sites,omitempty"`
	Verdict         *jsonVerdict   `json:"verdict,omitempty"`
	InheritedSource string         `json:"inherited_source,omitempty"`
	SentinelMissing bool           `json:"sentinel_missing,omitempty"`
}

type jsonSite struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Confidence string   `json:"line_confidence"`
	RawCode    string   `json:"raw_code,omitempty"`
	Append     bool     `json:"append,omitempty"`
	Modes      []string `json:"modes"`
}

type jsonVerdict struct {
	PerMode   map[string]jsonSite `json:"per_mode"`
	HasAppend bool                `json:"has_append,omitempty"`
}

func printJSON(w io.Writer, findings []Finding, opts Options) error {
	out := make([]jsonFinding, 0, len(findings))
	for _, f := range findings {
		inheritedSrc := f.InheritedSource
		if inheritedSrc != "" && !opts.ShowValue && !opts.FullValue {
			inheritedSrc = valueHidden // hide the value by default, like assignment RHS
		} else {
			inheritedSrc = sanitize(inheritedSrc) // S7
		}
		jf := jsonFinding{
			Name:            f.Name,
			Origin:          originString(f.Origin),
			InheritedSource: inheritedSrc,
			SentinelMissing: f.SentinelMissing,
		}
		for _, s := range f.Sites {
			js := siteToJSON(s, opts)
			jf.Sites = append(jf.Sites, js)
		}
		if f.Origin == Startup && len(f.Verdict.PerMode) > 0 {
			jv := &jsonVerdict{
				PerMode:   make(map[string]jsonSite),
				HasAppend: f.Verdict.HasAppend,
			}
			for mode, site := range f.Verdict.PerMode {
				// Winner block: omit RawCode (file:line + append note only, per A3).
				js := siteToJSON(*site, opts)
				js.RawCode = ""
				jv.PerMode[modeString(mode)] = js
			}
			jf.Verdict = jv
		}
		out = append(out, jf)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Output is consumed as JSON/CLI text, not embedded in HTML; keep '<', '>',
	// '&' literal (e.g. the "<hidden>" redaction marker) instead of \u00XX.
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

func siteToJSON(s AssignmentSite, opts Options) jsonSite {
	js := jsonSite{
		File:       sanitize(s.File),
		Line:       s.Line,
		Confidence: confidenceString(s.LineConf),
		RawCode:    displayRawCode(s.RawCode, opts),
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

// AnyStartupValue reports whether any finding has a startup site with an
// assignment whose value would be shown. Used by the CLI to decide whether to
// print the "values hidden" hint.
func AnyStartupValue(findings []Finding) bool {
	for _, f := range findings {
		if f.Origin == Startup {
			for _, s := range f.Sites {
				if strings.ContainsRune(s.RawCode, '=') {
					return true
				}
			}
		}
	}
	return false
}

func printOneFinding(w io.Writer, f Finding, opts Options) error {
	switch f.Origin {
	case Startup:
		return printStartup(w, f, opts)
	case Inherited:
		return printInherited(w, f, opts)
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
		return printStartupByMode(w, f, pal, opts)
	}
	return printStartupStack(w, f, pal, opts)
}

// printStartupStack renders the single-mode view like a stack trace: the most
// recent (effective) assignment first, older overridden ones below it.
func printStartupStack(w io.Writer, f Finding, pal palette, opts Options) error {
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
		if raw := displayRawCode(s.RawCode, opts); raw != "" {
			indent := "      "
			if n == 1 {
				indent = "    "
			}
			fmt.Fprintf(w, "%s%s\n", indent, pal.dim(raw))
		}
	}
	return nil
}

// printStartupByMode renders the --mode both view: each site tagged with the
// mode(s) it appeared in, plus per-mode winners (the answer differs by mode).
func printStartupByMode(w io.Writer, f Finding, pal palette, opts Options) error {
	fmt.Fprintf(w, "%s: set by startup\n", pal.name(f.Name))
	if f.SentinelMissing {
		fmt.Fprintln(w, pal.warn("  (warning: startup trace may be incomplete — shell exited before sentinel)"))
	}

	for _, s := range f.Sites {
		fileLine := formatFileLine(s)
		modes := formatModes(s.Modes)
		conf := formatConfidence(s.LineConf, s.ShellVersion)
		fmt.Fprintf(w, "  %s %s%s\n", pal.loc(fileLine), pal.dim("["+modes+"]"), pal.dim(conf))
		if raw := displayRawCode(s.RawCode, opts); raw != "" {
			fmt.Fprintf(w, "    %s\n", pal.dim(raw))
		}
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
			valuePart := ""
			if (opts.ShowValue || opts.FullValue) && site.RawCode != "" {
				valuePart = "  →  " + pal.dim(displayRawCode(site.RawCode, opts))
			}
			// Tab after the mode tag so file:line aligns across [login]/[non-login].
			fmt.Fprintf(w, "    [%s]\t%s%s%s%s\n", modeString(mode), pal.winner(fileLine), pal.dim(conf), appendNote, valuePart)
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

	if f.InheritedSource != "" {
		src := valueHidden
		if opts.ShowValue || opts.FullValue {
			src = sanitize(f.InheritedSource)
		}
		fmt.Fprintf(w, "  %s\n", pal.dim("→ set in the launchd session (launchctl: "+src+")"))
	} else {
		fmt.Fprintln(w, pal.dim("  → inherited from the parent process, or exported interactively / by a tool (these can't be traced)"))
	}
	return nil
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

// ── Display helpers ───────────────────────────────────────────────────────────

// displayRawCode applies the 3-level value rule to a raw assignment line:
//   - default (values hidden): redact the right-hand side → `export FOO=<hidden>`
//   - ShowValue: sanitized, truncated to maxRawCodeDefault runes with "…"
//   - FullValue: sanitized, full
func displayRawCode(raw string, opts Options) string {
	if raw == "" {
		return ""
	}
	if !opts.ShowValue && !opts.FullValue {
		return redactValue(raw)
	}
	s := sanitize(raw)
	if opts.FullValue {
		return s
	}
	runes := []rune(s)
	if len(runes) > maxRawCodeDefault {
		return string(runes[:maxRawCodeDefault]) + "…"
	}
	return s
}

// redactValue hides the right-hand side of an assignment, keeping the variable
// name and operator so the reader still sees HOW it was set without the value.
// `export FOO=secret` → `export FOO=<hidden>`; a valueless `export FOO` is
// returned unchanged (there is nothing to hide).
func redactValue(raw string) string {
	s := sanitize(raw)
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[:i+1] + valueHidden
	}
	return s
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
