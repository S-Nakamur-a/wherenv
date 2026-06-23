package report

import (
	"strings"
	"testing"

	"github.com/S-Nakamur-a/wherenv/internal/tracer"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeStartupFinding(name string, sites []AssignmentSite, perMode map[tracer.Mode]*AssignmentSite, hasAppend bool, sentinelMissing bool) Finding {
	return Finding{
		Name:   name,
		Origin: Startup,
		Sites:  sites,
		Verdict: Verdict{
			PerMode:   perMode,
			HasAppend: hasAppend,
		},
		SentinelMissing: sentinelMissing,
	}
}

func makeSite(file string, line int, rawCode string, append_ bool, modes ...tracer.Mode) AssignmentSite {
	return AssignmentSite{
		File:     file,
		Line:     line,
		LineConf: tracer.LineExact,
		RawCode:  rawCode,
		Append:   append_,
		Modes:    modes,
	}
}

func renderText(t *testing.T, findings []Finding, opts Options) string {
	t.Helper()
	var b strings.Builder
	if err := Print(&b, findings, opts); err != nil {
		t.Fatalf("Print: %v", err)
	}
	return b.String()
}

func renderJSON(t *testing.T, findings []Finding, opts Options) string {
	t.Helper()
	opts.JSON = true
	var b strings.Builder
	if err := Print(&b, findings, opts); err != nil {
		t.Fatalf("Print JSON: %v", err)
	}
	return b.String()
}

// ── B1: text output golden tests ──────────────────────────────────────────────

func TestPrintTextUnset(t *testing.T) {
	findings := []Finding{{Name: "NOTEXIST", Origin: Unset}}
	got := renderText(t, findings, Options{})
	want := "NOTEXIST: not set\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintTextInheritedNoSource(t *testing.T) {
	findings := []Finding{
		{Name: "SHLVL", Origin: Inherited},
	}
	got := renderText(t, findings, Options{})
	want := "SHLVL: present in the environment, not set by any startup file\n" +
		"  → inherited from the parent process, or exported interactively / by a tool (these can't be traced)\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTextInheritedWithSource(t *testing.T) {
	findings := []Finding{
		{Name: "TERM_PROGRAM", Origin: Inherited, InheritedSource: "iTerm.app"},
	}
	// Default hides the launchctl value.
	got := renderText(t, findings, Options{})
	if strings.Contains(got, "iTerm.app") {
		t.Errorf("default should hide the launchctl value; got %q", got)
	}
	if !strings.Contains(got, "launchctl: <hidden>") {
		t.Errorf("expected redacted launchctl source; got %q", got)
	}
	// --show-value reveals it.
	gotShow := renderText(t, findings, Options{ShowValue: true})
	if !strings.Contains(gotShow, "launchctl: iTerm.app") {
		t.Errorf("show-value should reveal launchctl source; got %q", gotShow)
	}
}

func TestPrintTextInheritedSentinelMissing(t *testing.T) {
	// An incomplete trace must NOT assert an external origin — it warns instead.
	findings := []Finding{
		{Name: "FOO", Origin: Inherited, SentinelMissing: true},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "present in the environment") {
		t.Errorf("expected environment-presence line; got %q", got)
	}
	if !strings.Contains(got, "INCOMPLETE") {
		t.Errorf("expected incomplete-trace warning; got %q", got)
	}
	if strings.Contains(got, "inherited from the parent process") {
		t.Errorf("must NOT assert external origin when trace incomplete; got %q", got)
	}
}

func TestPrintTextStartupSimple(t *testing.T) {
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{
				makeSite("/etc/zshrc", 5, "export FOO=hello", false, tracer.NonLogin),
			},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/etc/zshrc", Line: 5, LineConf: tracer.LineExact, RawCode: "export FOO=hello", Append: false, Modes: []tracer.Mode{tracer.NonLogin}},
			},
			false, false),
	}
	// Multi-mode (--mode both): value is hidden.
	got := renderText(t, findings, Options{ShowModes: true})
	want := "FOO: set by startup\n" +
		"  /etc/zshrc:5 [non-login]\n" +
		"    export FOO=<hidden>\n" +
		"  winner:\n" +
		"    [non-login]\t/etc/zshrc:5\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}

	// --show-value: real value appears, including on the winner line.
	gotShow := renderText(t, findings, Options{ShowValue: true, ShowModes: true})
	wantShow := "FOO: set by startup\n" +
		"  /etc/zshrc:5 [non-login]\n" +
		"    export FOO=hello\n" +
		"  winner:\n" +
		"    [non-login]\t/etc/zshrc:5  →  export FOO=hello\n"
	if gotShow != wantShow {
		t.Errorf("show-value got:\n%q\nwant:\n%q", gotShow, wantShow)
	}
}

func TestPrintTextStartupStack(t *testing.T) {
	// Facts only: assignments listed most-recent-first; the last-executed one is
	// marked "← ran last". No override/cumulative claims.
	winner := &AssignmentSite{File: "/c.zsh", Line: 21, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.Login}}
	findings := []Finding{
		makeStartupFinding("PATH",
			[]AssignmentSite{
				makeSite("/a.zsh", 3, "export PATH=/a", false, tracer.Login),
				makeSite("/b.zsh", 88, "export PATH=/a:/b", false, tracer.Login),
				makeSite("/c.zsh", 21, "export PATH=/a:/b:/c", false, tracer.Login),
			},
			map[tracer.Mode]*AssignmentSite{tracer.Login: winner},
			false, false),
	}
	got := renderText(t, findings, Options{})
	want := "PATH: set by startup  (3 places, most recent first)\n" +
		"\n" +
		"  → /c.zsh:21   ← ran last\n" +
		"      export PATH=<hidden>\n" +
		"    /b.zsh:88\n" +
		"      export PATH=<hidden>\n" +
		"    /a.zsh:3\n" +
		"      export PATH=<hidden>\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTextStartupMultiSite(t *testing.T) {
	// Site folded across both modes + separate login-only site.
	sharedSite := makeSite("/etc/zshrc", 5, "export FOO=x", false, tracer.NonLogin, tracer.Login)
	loginOnlySite := makeSite("/etc/zprofile", 2, "export FOO=z", false, tracer.Login)
	nonLoginWinner := &AssignmentSite{File: "/etc/zshrc", Line: 5, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.NonLogin}}
	loginWinner := &AssignmentSite{File: "/etc/zprofile", Line: 2, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.Login}}

	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{sharedSite, loginOnlySite},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: nonLoginWinner,
				tracer.Login:    loginWinner,
			},
			false, false),
	}
	got := renderText(t, findings, Options{ShowModes: true})
	want := "FOO: set by startup\n" +
		"  /etc/zshrc:5 [non-login+login]\n" +
		"    export FOO=<hidden>\n" +
		"  /etc/zprofile:2 [login]\n" +
		"    export FOO=<hidden>\n" +
		"  winner:\n" +
		"    [non-login]\t/etc/zshrc:5\n" +
		"    [login]\t/etc/zprofile:2\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTextStartupWithAppend(t *testing.T) {
	appendSite := makeSite("/home/u/.zshrc", 10, "PATH+=/extra", true, tracer.NonLogin)
	appendWinner := &AssignmentSite{File: "/home/u/.zshrc", Line: 10, LineConf: tracer.LineExact, Append: true, Modes: []tracer.Mode{tracer.NonLogin}}
	findings := []Finding{
		makeStartupFinding("PATH",
			[]AssignmentSite{appendSite},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: appendWinner},
			true, false),
	}
	got := renderText(t, findings, Options{ShowModes: true})
	want := "PATH: set by startup\n" +
		"  /home/u/.zshrc:10 [non-login]\n" +
		"    PATH+=<hidden>\n" +
		"    (appends with +=)\n" +
		"  winner:\n" +
		"    [non-login]\t/home/u/.zshrc:10 (+=)\n" +
		"    (chain includes += assignments)\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTextStartupSentinelMissing(t *testing.T) {
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{makeSite("/etc/zshrc", 1, "FOO=x", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/etc/zshrc", Line: 1, LineConf: tracer.LineExact},
			},
			false, true),
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "warning: startup trace may be incomplete") {
		t.Errorf("expected SentinelMissing warning; got %q", got)
	}
}

func TestPrintTextDefaultHidesValue(t *testing.T) {
	findings := []Finding{
		makeStartupFinding("SECRET",
			[]AssignmentSite{makeSite("/etc/zshrc", 3, "SECRET=hunter2", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/etc/zshrc", Line: 3, LineConf: tracer.LineExact, RawCode: "SECRET=hunter2"},
			},
			false, false),
	}
	// Default must never leak the value.
	got := renderText(t, findings, Options{})
	if strings.Contains(got, "hunter2") {
		t.Errorf("default output must NOT contain the secret value; got:\n%s", got)
	}
	if !strings.Contains(got, "SECRET=<hidden>") {
		t.Errorf("default output should redact as SECRET=<hidden>; got:\n%s", got)
	}
}

func TestPrintTextValueVerbosity(t *testing.T) {
	// Raw code longer than maxRawCodeDefault (120) to verify the 3 levels.
	longCode := "export FOO=" + strings.Repeat("a", 130)
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{makeSite("/etc/zshrc", 1, longCode, false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/etc/zshrc", Line: 1, LineConf: tracer.LineExact, RawCode: longCode},
			},
			false, false),
	}

	// Default: value hidden regardless of length.
	gotDefault := renderText(t, findings, Options{})
	if strings.Contains(gotDefault, "aaaa") {
		t.Errorf("default should hide the value; got %q", gotDefault)
	}
	if !strings.Contains(gotDefault, "export FOO=<hidden>") {
		t.Errorf("default should redact; got %q", gotDefault)
	}

	// --show-value: truncated at 120 runes with "…".
	gotShow := renderText(t, findings, Options{ShowValue: true})
	if !strings.Contains(gotShow, "…") {
		t.Errorf("show-value should truncate long values with '…'; got %q", gotShow)
	}
	for line := range strings.SplitSeq(gotShow, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "export FOO=") {
			if runes := []rune(trimmed); len(runes) > maxRawCodeDefault+1 { // +1 for the "…"
				t.Errorf("truncated line too long: %d runes, want ≤%d", len(runes), maxRawCodeDefault+1)
			}
		}
	}

	// --full-value: full string, no "…".
	gotFull := renderText(t, findings, Options{FullValue: true})
	if strings.Contains(gotFull, "…") {
		t.Errorf("full-value output should not truncate; got %q", gotFull)
	}
	if !strings.Contains(gotFull, longCode) {
		t.Errorf("full-value output should contain full raw code; got %q", gotFull)
	}
}

func TestPrintTextMultipleFindings(t *testing.T) {
	// Two findings: one Startup, one Unset; they are separated by a blank line.
	findings := []Finding{
		makeStartupFinding("A",
			[]AssignmentSite{makeSite("/f", 1, "A=1", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/f", Line: 1, LineConf: tracer.LineExact},
			},
			false, false),
		{Name: "B", Origin: Unset},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "\n\n") {
		t.Errorf("expected blank line separator between findings; got %q", got)
	}
}

// ── Step 4: Toolset text output tests ────────────────────────────────────────

func TestPrintTextToolsetWithFile(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/home/user/project/.envrc",
				Value: "hello",
			},
		},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "MY_VAR: set by direnv") {
		t.Errorf("expected 'set by direnv' header; got %q", got)
	}
	if !strings.Contains(got, "/home/user/project/.envrc") {
		t.Errorf("expected .envrc path; got %q", got)
	}
	if !strings.Contains(got, "directory-scoped") {
		t.Errorf("expected directory-scoped note; got %q", got)
	}
	// Value must be hidden by default.
	if strings.Contains(got, "hello") {
		t.Errorf("default output must not reveal the value; got %q", got)
	}
}

func TestPrintTextToolsetNoFile(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "",
				Value: "hello",
			},
		},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "DIRENV_FILE not set") {
		t.Errorf("expected DIRENV_FILE-not-set notice; got %q", got)
	}
}

func TestPrintTextToolsetShowValue(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/project/.envrc",
				Value: "secret",
			},
		},
	}
	got := renderText(t, findings, Options{ShowValue: true})
	if !strings.Contains(got, "secret") {
		t.Errorf("show-value should reveal the value; got %q", got)
	}
}

// TestPrintTextToolsetHeaderUsesTool verifies that the header line uses
// ToolSource.Tool, not a hardcoded string. This fixes the design invariant for
// future tools like mise: a Toolset finding with Tool="mise" must print
// "set by mise", not "set by direnv".
func TestPrintTextToolsetHeaderUsesTool(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MISE_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "mise",
				File:  "/project/mise.toml",
				Value: "val",
			},
		},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "set by mise") {
		t.Errorf("header must use ToolSource.Tool; got %q", got)
	}
	if strings.Contains(got, "set by direnv") {
		t.Errorf("header must not say 'set by direnv' for a non-direnv tool; got %q", got)
	}
}

// TestPrintTextToolsetValueControlChars verifies that S7 sanitize is applied
// to the tool value in text output (control characters become '?').
func TestPrintTextToolsetValueControlChars(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/project/.envrc",
				Value: "hello\x1b[31mworld",
			},
		},
	}
	got := renderText(t, findings, Options{ShowValue: true})
	if strings.Contains(got, "\x1b") {
		t.Errorf("control characters must be sanitized in text output; got %q", got)
	}
}

// TestPrintTextToolsetEmptyValueNotShown verifies that when ToolSource.Value is
// empty, no value line is emitted even with ShowValue=true.
func TestPrintTextToolsetEmptyValueNotShown(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/project/.envrc",
				Value: "",
			},
		},
	}
	got := renderText(t, findings, Options{ShowValue: true})
	// The "→  " value prefix must not appear when Value is empty.
	if strings.Contains(got, "→  ") {
		t.Errorf("empty Value must not produce a value line; got %q", got)
	}
}

// ── B1: JSON output golden tests ──────────────────────────────────────────────

func TestPrintJSONUnset(t *testing.T) {
	findings := []Finding{{Name: "GONE", Origin: Unset}}
	got := renderJSON(t, findings, Options{})
	want := `[
  {
    "name": "GONE",
    "origin": "unset"
  }
]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintJSONInherited(t *testing.T) {
	findings := []Finding{
		{Name: "TERM", Origin: Inherited, InheritedSource: "launchd"},
	}
	// Default redacts the launchctl value.
	got := renderJSON(t, findings, Options{})
	want := `[
  {
    "name": "TERM",
    "origin": "inherited",
    "inherited_source": "<hidden>"
  }
]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// --show-value reveals it.
	gotShow := renderJSON(t, findings, Options{ShowValue: true})
	if !strings.Contains(gotShow, `"inherited_source": "launchd"`) {
		t.Errorf("show-value JSON should reveal source; got:\n%s", gotShow)
	}
}

func TestPrintJSONStartupSingleMode(t *testing.T) {
	winner := &AssignmentSite{File: "/etc/zshrc", Line: 5, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.NonLogin}}
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{makeSite("/etc/zshrc", 5, "export FOO=x", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: winner},
			false, false),
	}
	got := renderJSON(t, findings, Options{})
	want := `[
  {
    "name": "FOO",
    "origin": "startup",
    "sites": [
      {
        "file": "/etc/zshrc",
        "line": 5,
        "line_confidence": "exact",
        "raw_code": "export FOO=<hidden>",
        "modes": [
          "non-login"
        ]
      }
    ],
    "verdict": {
      "per_mode": {
        "non-login": {
          "file": "/etc/zshrc",
          "line": 5,
          "line_confidence": "exact",
          "modes": [
            "non-login"
          ]
        }
      }
    }
  }
]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintJSONDefaultHidesValue(t *testing.T) {
	winner := &AssignmentSite{File: "/etc/zshrc", Line: 1, LineConf: tracer.LineExact}
	findings := []Finding{
		makeStartupFinding("SECRET",
			[]AssignmentSite{makeSite("/etc/zshrc", 1, "SECRET=pw", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: winner},
			false, false),
	}
	// Default JSON must redact the value, never leak it.
	got := renderJSON(t, findings, Options{})
	if strings.Contains(got, "SECRET=pw") || strings.Contains(got, `"pw"`) {
		t.Errorf("default JSON must not contain the secret value; got:\n%s", got)
	}
	if !strings.Contains(got, "SECRET=<hidden>") {
		t.Errorf("default JSON should redact value; got:\n%s", got)
	}
	// --show-value reveals it.
	gotShow := renderJSON(t, findings, Options{ShowValue: true})
	if !strings.Contains(gotShow, "SECRET=pw") {
		t.Errorf("show-value JSON should contain the value; got:\n%s", gotShow)
	}
}

func TestPrintJSONSentinelMissing(t *testing.T) {
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{makeSite("/f", 1, "FOO=x", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/f", Line: 1, LineConf: tracer.LineExact},
			},
			false, true),
	}
	got := renderJSON(t, findings, Options{})
	if !strings.Contains(got, `"sentinel_missing": true`) {
		t.Errorf("expected sentinel_missing in JSON; got:\n%s", got)
	}
}

// ── Step 5: Toolset JSON output tests ────────────────────────────────────────

func TestPrintJSONToolsetDefaultHidesValue(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/project/.envrc",
				Value: "secret",
			},
		},
	}
	got := renderJSON(t, findings, Options{})
	if !strings.Contains(got, `"origin": "toolset"`) {
		t.Errorf("expected origin=toolset in JSON; got:\n%s", got)
	}
	if !strings.Contains(got, `"tool": "direnv"`) {
		t.Errorf("expected tool=direnv in JSON; got:\n%s", got)
	}
	if strings.Contains(got, "secret") {
		t.Errorf("default JSON must not reveal the value; got:\n%s", got)
	}
	if !strings.Contains(got, `"value": "<hidden>"`) {
		t.Errorf("expected value=<hidden> in default JSON; got:\n%s", got)
	}
}

func TestPrintJSONToolsetShowValue(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/project/.envrc",
				Value: "secret",
			},
		},
	}
	got := renderJSON(t, findings, Options{ShowValue: true})
	if !strings.Contains(got, `"value": "secret"`) {
		t.Errorf("show-value JSON should reveal value; got:\n%s", got)
	}
}

func TestPrintJSONToolsetFileAlwaysSanitized(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool:  "direnv",
				File:  "/project\x1b[31m/.envrc",
				Value: "v",
			},
		},
	}
	got := renderJSON(t, findings, Options{})
	if strings.Contains(got, "\x1b") {
		t.Errorf("file path must be sanitized in JSON output; got:\n%s", got)
	}
}

// TestPrintJSONMapOrder verifies that per_mode keys are emitted in sorted order
// (encoding/json marshals map[string]... keys alphabetically — "login" < "non-login").
func TestPrintJSONMapOrder(t *testing.T) {
	nonLoginW := &AssignmentSite{File: "/etc/zshrc", Line: 1, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.NonLogin}}
	loginW := &AssignmentSite{File: "/etc/zprofile", Line: 2, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.Login}}
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{
				makeSite("/etc/zshrc", 1, "FOO=x", false, tracer.NonLogin),
				makeSite("/etc/zprofile", 2, "FOO=y", false, tracer.Login),
			},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: nonLoginW,
				tracer.Login:    loginW,
			},
			false, false),
	}
	got := renderJSON(t, findings, Options{})

	// Locate the per_mode block and inspect key order within it.
	perModeStart := strings.Index(got, `"per_mode"`)
	if perModeStart < 0 {
		t.Fatalf("expected per_mode in JSON; got:\n%s", got)
	}
	perModeSection := got[perModeStart:]
	loginIdx := strings.Index(perModeSection, `"login"`)
	nonLoginIdx := strings.Index(perModeSection, `"non-login"`)
	if loginIdx < 0 || nonLoginIdx < 0 {
		t.Fatalf("expected both 'login' and 'non-login' in per_mode; got:\n%s", perModeSection)
	}
	if loginIdx > nonLoginIdx {
		t.Errorf("expected 'login' to appear before 'non-login' in sorted JSON per_mode; got:\n%s", got)
	}
}

// ── B2: sanitize() tests ──────────────────────────────────────────────────────

func TestSanitize(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain ASCII passes through",
			input: "export FOO=bar",
			want:  "export FOO=bar",
		},
		{
			name:  "space and tab pass through",
			input: "FOO=a\tb c",
			want:  "FOO=a\tb c",
		},
		{
			name:  "ESC (ANSI) becomes question mark",
			input: "\x1b[31mred\x1b[0m",
			want:  "?[31mred?[0m",
		},
		{
			name:  "carriage return becomes question mark",
			input: "foo\rbar",
			want:  "foo?bar",
		},
		{
			name:  "C0 control chars (NUL, BEL, BS) become question marks",
			input: "\x00\x07\x08",
			want:  "???",
		},
		{
			name:  "newline is a C0 control — becomes question mark",
			input: "a\nb",
			want:  "a?b",
		},
		{
			name:  "printable Unicode passes through",
			input: "こんにちは",
			want:  "こんにちは",
		},
		{
			name:  "empty string is a no-op",
			input: "",
			want:  "",
		},
		{
			name:  "DEL (0x7f) is control — becomes question mark",
			input: "a\x7fb",
			want:  "a?b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize(tc.input)
			if got != tc.want {
				t.Errorf("sanitize(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeInvalidUTF8(t *testing.T) {
	// An invalid UTF-8 byte sequence: Go's range over string will produce
	// the Unicode replacement character (U+FFFD) for invalid bytes.
	// U+FFFD is not a control character and is printable, so it passes through.
	input := string([]byte{0xFF, 0xFE})
	got := sanitize(input)
	// Both 0xFF and 0xFE are decoded as U+FFFD (replacement char) — not control — pass through.
	if strings.ContainsAny(got, "?") {
		// This is the observed behavior: invalid UTF-8 → U+FFFD replacement char → passes through.
		// If the implementation replaces with '?' instead, this test documents that fact.
		t.Logf("invalid UTF-8 sanitized to %q (contains '?') — acceptable", got)
	}
	// Just verify no panic and output is non-empty.
	if len(got) == 0 {
		t.Error("sanitize of non-empty invalid UTF-8 should not return empty string")
	}
}
