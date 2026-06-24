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

// makeSite keeps a rawCode parameter for call-site readability, but the value is
// intentionally not stored: AssignmentSite no longer carries it (wherenv never
// holds variable values).
func makeSite(file string, line int, _ string, append_ bool, modes ...tracer.Mode) AssignmentSite {
	return AssignmentSite{
		File:     file,
		Line:     line,
		LineConf: tracer.LineExact,
		Append:   append_,
		Modes:    modes,
	}
}

// renderText exercises the human-readable formatter. The default format is now
// TSV, so these golden tests opt in via Human: true.
func renderText(t *testing.T, findings []Finding, opts Options) string {
	t.Helper()
	opts.Human = true
	var b strings.Builder
	if err := Print(&b, findings, opts); err != nil {
		t.Fatalf("Print: %v", err)
	}
	return b.String()
}

// renderTSV exercises the default machine-readable TSV formatter.
func renderTSV(t *testing.T, findings []Finding) string {
	t.Helper()
	var b strings.Builder
	if err := Print(&b, findings, Options{}); err != nil {
		t.Fatalf("Print TSV: %v", err)
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

func TestPrintTextInheritedFromLaunchd(t *testing.T) {
	findings := []Finding{
		{Name: "TERM_PROGRAM", Origin: Inherited, InheritedFromLaunchd: true},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "set in the launchd session") {
		t.Errorf("expected launchd-session provenance line; got %q", got)
	}
	// No value is ever held, so the launchctl value cannot leak.
	if strings.Contains(got, "launchctl:") {
		t.Errorf("output must not present a launchctl value; got %q", got)
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
				tracer.NonLogin: {File: "/etc/zshrc", Line: 5, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.NonLogin}},
			},
			false, false),
	}
	// Multi-mode (--mode both): location only, never the value.
	got := renderText(t, findings, Options{ShowModes: true})
	want := "FOO: set by startup\n" +
		"  /etc/zshrc:5 [non-login]\n" +
		"  winner:\n" +
		"    [non-login]\t/etc/zshrc:5\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTextStartupVia(t *testing.T) {
	// An assignment performed inside a helper function (envsource) carries a
	// caller location; the caller becomes primary and the helper is the via note.
	site := AssignmentSite{
		File: "/home/u/.config/zsh/functions/envsource", Line: 16,
		LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.Login},
		CallerFile: "/home/u/.config/zsh/conf.d/08-profile.zsh", CallerLine: 7,
	}
	findings := []Finding{
		makeStartupFinding("AWS_PROFILE",
			[]AssignmentSite{site},
			map[tracer.Mode]*AssignmentSite{tracer.Login: &site},
			false, false),
	}
	got := renderText(t, findings, Options{})
	want := "AWS_PROFILE: set by startup\n" +
		"  /home/u/.config/zsh/conf.d/08-profile.zsh:7  (via /home/u/.config/zsh/functions/envsource:16)\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintJSONStartupVia(t *testing.T) {
	site := AssignmentSite{
		File: "/home/u/functions/envsource", Line: 16,
		LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.Login},
		CallerFile: "/home/u/conf.d/08.zsh", CallerLine: 7,
	}
	findings := []Finding{
		makeStartupFinding("AWS_PROFILE", []AssignmentSite{site}, nil, false, false),
	}
	got := renderJSON(t, findings, Options{})
	if !strings.Contains(got, `"caller_file": "/home/u/conf.d/08.zsh"`) {
		t.Errorf("expected caller_file in JSON; got:\n%s", got)
	}
	if !strings.Contains(got, `"caller_line": 7`) {
		t.Errorf("expected caller_line in JSON; got:\n%s", got)
	}
}

func TestPrintTextStartupStack(t *testing.T) {
	// Facts only: assignments listed most-recent-first; the last-executed one is
	// marked "← ran last". No override/cumulative claims, no values.
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
		"    /b.zsh:88\n" +
		"    /a.zsh:3\n"
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
		"  /etc/zprofile:2 [login]\n" +
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

// TestPrintTextNeverShowsValue is a regression fence: even when a finding is
// built from a "SECRET=hunter2" assignment, the value is dropped at capture time
// and so can never appear in the formatted output.
func TestPrintTextNeverShowsValue(t *testing.T) {
	findings := []Finding{
		makeStartupFinding("SECRET",
			[]AssignmentSite{makeSite("/etc/zshrc", 3, "SECRET=hunter2", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{
				tracer.NonLogin: {File: "/etc/zshrc", Line: 3, LineConf: tracer.LineExact},
			},
			false, false),
	}
	got := renderText(t, findings, Options{})
	if strings.Contains(got, "hunter2") {
		t.Errorf("output must never contain the secret value; got:\n%s", got)
	}
	// And no assignment RHS marker should appear at all.
	if strings.Contains(got, "SECRET=") {
		t.Errorf("output must not render an assignment line; got:\n%s", got)
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

// ── Toolset text output tests ────────────────────────────────────────────────

func TestPrintTextToolsetWithFile(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "direnv",
				File: "/home/user/project/.envrc",
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
}

func TestPrintTextToolsetNoFile(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "direnv",
				File: "",
			},
		},
	}
	got := renderText(t, findings, Options{})
	// When File is empty the output must describe unavailability using the tool name,
	// without any direnv-specific keys like "DIRENV_FILE".
	if !strings.Contains(got, "source path unavailable") {
		t.Errorf("expected 'source path unavailable' notice; got %q", got)
	}
	if !strings.Contains(got, "set by direnv") {
		t.Errorf("expected 'set by direnv' in unavailable-source line; got %q", got)
	}
	if strings.Contains(got, "DIRENV_FILE") {
		t.Errorf("output must not reference DIRENV_FILE (tool-agnostic format); got %q", got)
	}
}

// TestPrintTextToolsetHeaderUsesTool verifies that the header line uses
// ToolSource.Tool, not a hardcoded string. A Toolset finding with Tool="mise"
// must print "set by mise", not "set by direnv".
func TestPrintTextToolsetHeaderUsesTool(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MISE_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "mise",
				File: "/project/mise.toml",
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

// ── mise-specific Toolset text output tests ──────────────────────────────────

// TestPrintTextToolsetMiseWithFile verifies that a mise Toolset finding
// renders "set by mise" + the mise.toml path + the mise scope note, and does
// NOT contain any direnv-specific strings.
func TestPrintTextToolsetMiseWithFile(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_MISE_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "mise",
				File: "/project/mise.toml",
			},
		},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "MY_MISE_VAR: set by mise") {
		t.Errorf("expected 'set by mise' header; got %q", got)
	}
	if !strings.Contains(got, "/project/mise.toml") {
		t.Errorf("expected mise.toml path in output; got %q", got)
	}
	if !strings.Contains(got, "directory-scoped") {
		t.Errorf("expected 'directory-scoped' in scope note; got %q", got)
	}
	// Must NOT contain direnv-specific strings.
	if strings.Contains(got, ".envrc") {
		t.Errorf("mise output must not mention .envrc; got %q", got)
	}
	if strings.Contains(got, "DIRENV") {
		t.Errorf("mise output must not mention DIRENV; got %q", got)
	}
}

// TestPrintTextToolsetMiseNoFile verifies the File=="" code path for mise:
// the output must use the tool name, not reference DIRENV_FILE.
func TestPrintTextToolsetMiseNoFile(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_MISE_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "mise",
				File: "",
			},
		},
	}
	got := renderText(t, findings, Options{})
	if !strings.Contains(got, "source path unavailable") {
		t.Errorf("expected 'source path unavailable'; got %q", got)
	}
	if strings.Contains(got, ".envrc") {
		t.Errorf("mise no-file output must not mention .envrc; got %q", got)
	}
	if strings.Contains(got, "DIRENV") {
		t.Errorf("mise no-file output must not mention DIRENV; got %q", got)
	}
}

// ── TSV output golden tests (default, machine-readable) ───────────────────────

func TestPrintTSVUnset(t *testing.T) {
	findings := []Finding{{Name: "NOTEXIST", Origin: Unset}}
	got := renderTSV(t, findings)
	want := "NOTEXIST\tunset\t\t\t\t\t\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVInheritedNoSource(t *testing.T) {
	findings := []Finding{{Name: "SHLVL", Origin: Inherited}}
	got := renderTSV(t, findings)
	want := "SHLVL\tinherited\t\t\t\t\t\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVInheritedLaunchd(t *testing.T) {
	findings := []Finding{{Name: "TERM", Origin: Inherited, InheritedFromLaunchd: true}}
	got := renderTSV(t, findings)
	want := "TERM\tinherited\t\t\t\t\tlaunchd\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVInheritedSentinelMissing(t *testing.T) {
	findings := []Finding{{Name: "FOO", Origin: Inherited, SentinelMissing: true}}
	got := renderTSV(t, findings)
	want := "FOO\tinherited\t\t\t\t\tincomplete\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVStartupSingleSiteWinner(t *testing.T) {
	winner := &AssignmentSite{File: "/etc/zshrc", Line: 5, LineConf: tracer.LineExact, Modes: []tracer.Mode{tracer.NonLogin}}
	findings := []Finding{
		makeStartupFinding("FOO",
			[]AssignmentSite{makeSite("/etc/zshrc", 5, "export FOO=x", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: winner},
			false, false),
	}
	got := renderTSV(t, findings)
	want := "FOO\tstartup\t/etc/zshrc\t5\texact\tnon-login\twinner=non-login\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVStartupMultiSite(t *testing.T) {
	// Three sites; the last (in execution order) is the winner. One line per site,
	// in execution order, so callers can grep by file / cut the line number.
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
	got := renderTSV(t, findings)
	want := "PATH\tstartup\t/a.zsh\t3\texact\tlogin\t\t\t\n" +
		"PATH\tstartup\t/b.zsh\t88\texact\tlogin\t\t\t\n" +
		"PATH\tstartup\t/c.zsh\t21\texact\tlogin\twinner=login\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVStartupAppendAndModes(t *testing.T) {
	appendSite := makeSite("/home/u/.zshrc", 10, "PATH+=/extra", true, tracer.NonLogin, tracer.Login)
	winner := &AssignmentSite{File: "/home/u/.zshrc", Line: 10, LineConf: tracer.LineExact, Append: true, Modes: []tracer.Mode{tracer.NonLogin}}
	findings := []Finding{
		makeStartupFinding("PATH",
			[]AssignmentSite{appendSite},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: winner},
			true, false),
	}
	got := renderTSV(t, findings)
	want := "PATH\tstartup\t/home/u/.zshrc\t10\texact\tnon-login+login\twinner=non-login,append\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrintTSVToolset(t *testing.T) {
	findings := []Finding{
		{Name: "MY_VAR", Origin: Toolset, ToolSource: &ToolSource{Tool: "direnv", File: "/proj/.envrc"}},
	}
	got := renderTSV(t, findings)
	want := "MY_VAR\ttoolset\t/proj/.envrc\t\t\t\ttool=direnv\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestPrintTSVStartupVia checks the caller columns (8/9) carry the call site for
// a helper-mediated assignment, while file/line (3/4) stay the mechanism.
func TestPrintTSVStartupVia(t *testing.T) {
	site := AssignmentSite{
		File: "/home/u/functions/envsource", Line: 16, LineConf: tracer.LineExact,
		Modes:      []tracer.Mode{tracer.Login},
		CallerFile: "/home/u/conf.d/08.zsh", CallerLine: 7,
	}
	findings := []Finding{
		makeStartupFinding("AWS_PROFILE", []AssignmentSite{site},
			map[tracer.Mode]*AssignmentSite{tracer.Login: &site}, false, false),
	}
	got := renderTSV(t, findings)
	want := "AWS_PROFILE\tstartup\t/home/u/functions/envsource\t16\texact\tlogin\twinner=login\t/home/u/conf.d/08.zsh\t7\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestPrintTSVFieldsAreTabSafe verifies a tab embedded in a file path can never
// split a record into spurious columns: it is neutralized to a space.
func TestPrintTSVFieldsAreTabSafe(t *testing.T) {
	findings := []Finding{
		{Name: "MY_VAR", Origin: Toolset, ToolSource: &ToolSource{Tool: "direnv", File: "/proj\tevil/.envrc"}},
	}
	got := renderTSV(t, findings)
	want := "MY_VAR\ttoolset\t/proj evil/.envrc\t\t\t\ttool=direnv\t\t\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	// Exactly 8 tabs → 9 columns, regardless of the path content.
	if n := strings.Count(strings.TrimRight(got, "\n"), "\t"); n != 8 {
		t.Errorf("expected 8 tab separators (9 columns), got %d in %q", n, got)
	}
}

// TestPrintTSVNeverShowsValue is a regression fence: even built from a
// "SECRET=hunter2" assignment, the TSV must never carry the value.
func TestPrintTSVNeverShowsValue(t *testing.T) {
	winner := &AssignmentSite{File: "/etc/zshrc", Line: 3, LineConf: tracer.LineExact}
	findings := []Finding{
		makeStartupFinding("SECRET",
			[]AssignmentSite{makeSite("/etc/zshrc", 3, "SECRET=hunter2", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: winner},
			false, false),
	}
	got := renderTSV(t, findings)
	if strings.Contains(got, "hunter2") {
		t.Errorf("TSV must never contain the secret value; got:\n%s", got)
	}
	if strings.Contains(got, "SECRET=") {
		t.Errorf("TSV must not render an assignment RHS; got:\n%s", got)
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
		{Name: "TERM", Origin: Inherited, InheritedFromLaunchd: true},
	}
	got := renderJSON(t, findings, Options{})
	want := `[
  {
    "name": "TERM",
    "origin": "inherited",
    "inherited_from_launchd": true
  }
]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
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

// TestPrintJSONNeverShowsValue is a regression fence: the JSON output must
// never carry a value or raw_code, regardless of the source assignment.
func TestPrintJSONNeverShowsValue(t *testing.T) {
	winner := &AssignmentSite{File: "/etc/zshrc", Line: 1, LineConf: tracer.LineExact}
	findings := []Finding{
		makeStartupFinding("SECRET",
			[]AssignmentSite{makeSite("/etc/zshrc", 1, "SECRET=pw", false, tracer.NonLogin)},
			map[tracer.Mode]*AssignmentSite{tracer.NonLogin: winner},
			false, false),
	}
	got := renderJSON(t, findings, Options{})
	if strings.Contains(got, "pw") {
		t.Errorf("JSON must not contain the secret value; got:\n%s", got)
	}
	if strings.Contains(got, "raw_code") || strings.Contains(got, "\"value\"") {
		t.Errorf("JSON must not carry raw_code or value fields; got:\n%s", got)
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

// ── Toolset JSON output tests ────────────────────────────────────────────────

func TestPrintJSONToolset(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "direnv",
				File: "/project/.envrc",
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
	if strings.Contains(got, `"value"`) {
		t.Errorf("JSON tool_source must not carry a value field; got:\n%s", got)
	}
}

func TestPrintJSONToolsetFileAlwaysSanitized(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "direnv",
				File: "/project\x1b[31m/.envrc",
			},
		},
	}
	got := renderJSON(t, findings, Options{})
	if strings.Contains(got, "\x1b") {
		t.Errorf("file path must be sanitized in JSON output; got:\n%s", got)
	}
}

// TestPrintJSONToolsetMise verifies that a mise ToolSource produces
// origin:"toolset", tool_source.tool:"mise", and the source file, with no value.
func TestPrintJSONToolsetMise(t *testing.T) {
	findings := []Finding{
		{
			Name:   "MY_MISE_VAR",
			Origin: Toolset,
			ToolSource: &ToolSource{
				Tool: "mise",
				File: "/project/mise.toml",
			},
		},
	}
	got := renderJSON(t, findings, Options{})
	if !strings.Contains(got, `"origin": "toolset"`) {
		t.Errorf("expected origin=toolset in JSON; got:\n%s", got)
	}
	if !strings.Contains(got, `"tool": "mise"`) {
		t.Errorf("expected tool=mise in JSON; got:\n%s", got)
	}
	if !strings.Contains(got, `"file": "/project/mise.toml"`) {
		t.Errorf("expected file=mise.toml in JSON; got:\n%s", got)
	}
	if strings.Contains(got, `"value"`) {
		t.Errorf("JSON tool_source must not carry a value field; got:\n%s", got)
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
	if len(got) == 0 {
		t.Error("sanitize of non-empty invalid UTF-8 should not return empty string")
	}
}
