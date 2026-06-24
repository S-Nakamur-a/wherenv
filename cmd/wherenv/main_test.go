package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/S-Nakamur-a/wherenv/internal/report"
)

// runCapture is a test helper that calls run() with isolated stdout/stderr.
func runCapture(args []string, shellEnv string) (code int, stdout, stderr string) {
	var outBuf, errBuf strings.Builder
	getenv := func(key string) string {
		if key == "SHELL" {
			return shellEnv
		}
		return ""
	}
	code = run(args, getenv, &outBuf, &errBuf)
	return code, outBuf.String(), errBuf.String()
}

// ── S1: key validation (B3) ───────────────────────────────────────────────────

func TestRunInvalidKeyEmpty(t *testing.T) {
	code, _, errOut := runCapture([]string{""}, "/bin/zsh")
	if code != 2 {
		t.Errorf("empty key: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "invalid variable name") {
		t.Errorf("expected 'invalid variable name' in stderr; got %q", errOut)
	}
}

func TestRunInvalidKeyStartsWithDigit(t *testing.T) {
	code, _, errOut := runCapture([]string{"1abc"}, "/bin/zsh")
	if code != 2 {
		t.Errorf("digit-leading key: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "invalid variable name") {
		t.Errorf("expected 'invalid variable name' in stderr; got %q", errOut)
	}
}

func TestRunInvalidKeySemicolon(t *testing.T) {
	code, _, errOut := runCapture([]string{"FOO;BAR"}, "/bin/zsh")
	if code != 2 {
		t.Errorf("semicolon key: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "invalid variable name") {
		t.Errorf("expected 'invalid variable name' in stderr; got %q", errOut)
	}
}

func TestRunInvalidKeySpace(t *testing.T) {
	code, _, errOut := runCapture([]string{"FOO BAR"}, "/bin/zsh")
	if code != 2 {
		t.Errorf("space in key: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "invalid variable name") {
		t.Errorf("expected 'invalid variable name' in stderr; got %q", errOut)
	}
}

func TestRunInvalidKeyLeadingHyphen(t *testing.T) {
	// A leading hyphen looks like a flag; the FlagSet will return an error.
	// The exact error message differs but exit code must be 2.
	code, _, _ := runCapture([]string{"-BADKEY"}, "/bin/zsh")
	if code != 2 {
		t.Errorf("leading-hyphen key: want exit 2, got %d", code)
	}
}

// TestRunNoArgsTracesAllEnv verifies the no-argument behavior: instead of
// printing usage, wherenv traces every variable visible in the environment. We
// point SHELL at an unsupported shell so no real shell is spawned — classification
// falls back to env-only, and every present variable is reported as inherited.
func TestRunNoArgsTracesAllEnv(t *testing.T) {
	// A uniquely-named variable we control, so the assertion can't be satisfied by
	// some unrelated entry in the test process environment.
	t.Setenv("WHERENV_NOARGS_TEST_VAR", "x")

	code, outStr, errOut := runCapture([]string{}, "/usr/local/bin/fish")
	if code != 0 {
		t.Fatalf("no args: want exit 0, got %d (stderr: %s)", code, errOut)
	}
	// The variable we set must appear as an inherited TSV record, proving the
	// no-arg run enumerated and classified the whole environment.
	if !strings.Contains(outStr, "WHERENV_NOARGS_TEST_VAR\tinherited\t") {
		t.Errorf("expected no-arg run to trace all env vars incl. WHERENV_NOARGS_TEST_VAR; got %q", outStr)
	}
}

// TestEnvKeysSortsAndFiltersInvalid verifies that envKeys returns only valid
// identifiers, sorted, so the no-arg trace covers a predictable, S1-safe set.
func TestEnvKeysSortsAndFiltersInvalid(t *testing.T) {
	snap := map[string]string{
		"ZED":           "",
		"PATH":          "",
		"alpha":         "",
		"BASH_FUNC_x%%": "", // bash exported-function entry: not a valid identifier
		"HAS SPACE":     "", // not a valid identifier
		"_UNDERSCORE":   "",
	}
	got := envKeys(snap)
	want := []string{"PATH", "ZED", "_UNDERSCORE", "alpha"}
	if len(got) != len(want) {
		t.Fatalf("envKeys: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("envKeys: got %v, want %v", got, want)
		}
	}
}

// ── Unsupported shell degrades gracefully (B3) ────────────────────────────────

func TestRunUnsupportedShellDegrades(t *testing.T) {
	// With shell=fish, no tracer runs; we fall through to classify from env.
	// With an env that has no NOTEXIST key (getenv returns ""), it should be Unset.
	code, outStr, errOut := runCapture([]string{"NOTEXIST_WHERENV_B3"}, "/usr/local/bin/fish")
	// Should succeed (exit 0) but warn about unsupported shell.
	if code != 0 {
		t.Errorf("unsupported shell: want exit 0, got %d (stderr: %s)", code, errOut)
	}
	if !strings.Contains(errOut, "unsupported shell") {
		t.Errorf("expected 'unsupported shell' warning in stderr; got %q", errOut)
	}
	// Default output is machine-readable TSV: the Unset origin renders as a
	// "<name>\tunset\t..." record rather than the human-readable "not set".
	if !strings.Contains(outStr, "NOTEXIST_WHERENV_B3\tunset\t") {
		t.Errorf("expected TSV unset record in stdout for unset variable; got %q", outStr)
	}
}

// ── --timeout validation ──────────────────────────────────────────────────────

func TestRunTimeoutZeroIsRejected(t *testing.T) {
	code, _, errOut := runCapture([]string{"--timeout", "0", "FOO"}, "/usr/local/bin/fish")
	if code != 2 {
		t.Errorf("--timeout 0: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "--timeout must be > 0") {
		t.Errorf("expected timeout error in stderr; got %q", errOut)
	}
}

func TestRunTimeoutNegativeIsRejected(t *testing.T) {
	code, _, errOut := runCapture([]string{"--timeout", "-1", "FOO"}, "/usr/local/bin/fish")
	if code != 2 {
		t.Errorf("--timeout -1: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "--timeout must be > 0") {
		t.Errorf("expected timeout error in stderr; got %q", errOut)
	}
}

// ── elevateOrigins unit tests (exec-free) ────────────────────────────────────

// makeDiffForMain encodes prev/next maps as a DIRENV_DIFF value (zlib+base64url)
// so these tests do not require direnv to be installed.
func makeDiffForMain(t *testing.T, next map[string]string) string {
	t.Helper()
	payload := map[string]map[string]string{"p": {}, "n": next}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(jsonBytes); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return base64.URLEncoding.EncodeToString(buf.Bytes())
}

// TestElevateOriginsStartupNotTouched verifies the most important invariant:
// a Startup variable that also appears in DIRENV_DIFF n is NOT elevated to
// Toolset. direnv must never override startup-traced provenance.
func TestElevateOriginsStartupNotTouched(t *testing.T) {
	diff := makeDiffForMain(t, map[string]string{"MY_VAR": "hello"})
	snap := map[string]string{
		"DIRENV_DIFF": diff,
		"DIRENV_FILE": "/project/.envrc",
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Startup},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, map[string]report.ToolSource{}, func(name string) bool {
		launchctlCalls++
		return false
	})
	if findings[0].Origin != report.Startup {
		t.Errorf("Startup variable must not be elevated; got Origin=%v", findings[0].Origin)
	}
	if findings[0].ToolSource != nil {
		t.Error("Startup variable must not have ToolSource set")
	}
	if launchctlCalls != 0 {
		t.Errorf("launchctlProbe must not be called for Startup variables; called %d time(s)", launchctlCalls)
	}
}

// TestElevateOriginsDirenvHit verifies that an Inherited variable present in
// DIRENV_DIFF n is elevated to Toolset with correct ToolSource fields.
func TestElevateOriginsDirenvHit(t *testing.T) {
	diff := makeDiffForMain(t, map[string]string{"MY_VAR": "hello"})
	snap := map[string]string{
		"DIRENV_DIFF": diff,
		"DIRENV_FILE": "/project/.envrc",
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Inherited},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, map[string]report.ToolSource{}, func(name string) bool {
		launchctlCalls++
		return true
	})
	if findings[0].Origin != report.Toolset {
		t.Errorf("direnv-hit Inherited variable must be elevated to Toolset; got %v", findings[0].Origin)
	}
	if findings[0].ToolSource == nil {
		t.Fatal("ToolSource must be set for a Toolset finding")
	}
	if findings[0].ToolSource.Tool != "direnv" {
		t.Errorf("ToolSource.Tool: got %q, want %q", findings[0].ToolSource.Tool, "direnv")
	}
	// direnv hit must skip the launchctl probe and mise probe entirely.
	if launchctlCalls != 0 {
		t.Errorf("launchctlProbe must NOT be called when direnv probe hits; called %d time(s)", launchctlCalls)
	}
}

// TestElevateOriginsDirenvMissLaunchctlCalled verifies that when the direnv
// probe does not match, the launchctlProbe is called and its presence result
// recorded in InheritedFromLaunchd.
func TestElevateOriginsDirenvMissLaunchctlCalled(t *testing.T) {
	// DIRENV_DIFF contains a different variable, so MY_VAR is a miss.
	diff := makeDiffForMain(t, map[string]string{"OTHER": "val"})
	snap := map[string]string{
		"DIRENV_DIFF": diff,
		"DIRENV_FILE": "/project/.envrc",
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Inherited},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, map[string]report.ToolSource{}, func(name string) bool {
		launchctlCalls++
		return true
	})
	if findings[0].Origin != report.Inherited {
		t.Errorf("direnv-miss variable must stay Inherited; got %v", findings[0].Origin)
	}
	if launchctlCalls != 1 {
		t.Errorf("launchctlProbe must be called exactly once on direnv miss; called %d time(s)", launchctlCalls)
	}
	if !findings[0].InheritedFromLaunchd {
		t.Error("InheritedFromLaunchd must be true when launchctl probe reports presence")
	}
}

// ── elevateOrigins mise tests ─────────────────────────────────────────────────

// TestElevateOriginsMiseHit verifies that an Inherited variable present in
// miseSources is elevated to Toolset with Tool=="mise" and that launchctl
// is not called (mise probe already resolved the variable).
func TestElevateOriginsMiseHit(t *testing.T) {
	snap := map[string]string{} // no DIRENV_DIFF, so direnv probe always misses
	miseSources := map[string]report.ToolSource{
		"MY_MISE_VAR": {Tool: "mise", File: "/project/mise.toml"},
	}
	findings := []report.Finding{
		{Name: "MY_MISE_VAR", Origin: report.Inherited},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, miseSources, func(name string) bool {
		launchctlCalls++
		return false
	})
	if findings[0].Origin != report.Toolset {
		t.Errorf("mise-hit variable must be elevated to Toolset; got %v", findings[0].Origin)
	}
	if findings[0].ToolSource == nil {
		t.Fatal("ToolSource must be set for mise-hit finding")
	}
	if findings[0].ToolSource.Tool != "mise" {
		t.Errorf("Tool: got %q, want %q", findings[0].ToolSource.Tool, "mise")
	}
	if findings[0].ToolSource.File != "/project/mise.toml" {
		t.Errorf("File: got %q, want %q", findings[0].ToolSource.File, "/project/mise.toml")
	}
	// mise hit must skip the launchctl probe.
	if launchctlCalls != 0 {
		t.Errorf("launchctlProbe must NOT be called when mise probe hits; called %d time(s)", launchctlCalls)
	}
}

// TestElevateOriginsDirenvPriority verifies that when a variable appears in
// both DIRENV_DIFF and miseSources, direnv wins (it is checked first) and
// the mise path is never reached.
func TestElevateOriginsDirenvPriority(t *testing.T) {
	diff := makeDiffForMain(t, map[string]string{"MY_VAR": "direnv-val"})
	snap := map[string]string{
		"DIRENV_DIFF": diff,
		"DIRENV_FILE": "/project/.envrc",
	}
	// Same variable is also in miseSources — direnv must win.
	miseSources := map[string]report.ToolSource{
		"MY_VAR": {Tool: "mise", File: "/project/mise.toml"},
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Inherited},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, miseSources, func(name string) bool {
		launchctlCalls++
		return false
	})
	if findings[0].Origin != report.Toolset {
		t.Errorf("direnv-priority variable must be Toolset; got %v", findings[0].Origin)
	}
	if findings[0].ToolSource == nil {
		t.Fatal("ToolSource must be set")
	}
	if findings[0].ToolSource.Tool != "direnv" {
		t.Errorf("Tool must be direnv (priority over mise); got %q", findings[0].ToolSource.Tool)
	}
	if launchctlCalls != 0 {
		t.Errorf("launchctlProbe must not be called when direnv hits; called %d time(s)", launchctlCalls)
	}
}

// TestElevateOriginsMiseMissLaunchctlCalled verifies that when neither direnv
// nor mise matches, the launchctl probe is called.
func TestElevateOriginsMiseMissLaunchctlCalled(t *testing.T) {
	snap := map[string]string{} // no DIRENV_DIFF
	miseSources := map[string]report.ToolSource{
		"OTHER_VAR": {Tool: "mise", File: "/project/mise.toml"},
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Inherited},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, miseSources, func(name string) bool {
		launchctlCalls++
		return true
	})
	if findings[0].Origin != report.Inherited {
		t.Errorf("mise-miss variable must stay Inherited; got %v", findings[0].Origin)
	}
	if launchctlCalls != 1 {
		t.Errorf("launchctlProbe must be called exactly once on mise miss; called %d time(s)", launchctlCalls)
	}
	if !findings[0].InheritedFromLaunchd {
		t.Error("InheritedFromLaunchd must be true when launchctl probe reports presence")
	}
}

// TestElevateOriginsStartupNotTouchedByMise verifies the critical invariant:
// a Startup variable that also appears in miseSources is NOT elevated to
// Toolset. mise must never override startup-traced provenance.
func TestElevateOriginsStartupNotTouchedByMise(t *testing.T) {
	snap := map[string]string{}
	miseSources := map[string]report.ToolSource{
		"MY_VAR": {Tool: "mise", File: "/project/mise.toml"},
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Startup},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, miseSources, func(name string) bool {
		launchctlCalls++
		return false
	})
	if findings[0].Origin != report.Startup {
		t.Errorf("Startup variable must not be elevated by mise; got Origin=%v", findings[0].Origin)
	}
	if findings[0].ToolSource != nil {
		t.Error("Startup variable must not have ToolSource set")
	}
	if launchctlCalls != 0 {
		t.Errorf("launchctlProbe must not be called for Startup variables; called %d time(s)", launchctlCalls)
	}
}

// TestElevateOriginsUnsetNotTouchedByMise verifies the Unset invariant:
// an Unset variable that appears in miseSources must NOT be elevated to Toolset.
// Only Inherited findings are eligible for elevation.
func TestElevateOriginsUnsetNotTouchedByMise(t *testing.T) {
	snap := map[string]string{}
	miseSources := map[string]report.ToolSource{
		"MY_VAR": {Tool: "mise", File: "/project/mise.toml"},
	}
	findings := []report.Finding{
		{Name: "MY_VAR", Origin: report.Unset},
	}
	launchctlCalls := 0
	elevateOrigins(findings, snap, miseSources, func(name string) bool {
		launchctlCalls++
		return false
	})
	if findings[0].Origin != report.Unset {
		t.Errorf("Unset variable must not be elevated by mise; got Origin=%v", findings[0].Origin)
	}
	if findings[0].ToolSource != nil {
		t.Error("Unset variable must not have ToolSource set")
	}
	if launchctlCalls != 0 {
		t.Errorf("launchctlProbe must not be called for Unset variables; called %d time(s)", launchctlCalls)
	}
}

// ── Valid key with unsupported shell (smoke) ──────────────────────────────────

func TestRunValidKeyNoError(t *testing.T) {
	// A valid key name with unsupported shell should not return exit 2 due to validation.
	code, _, errOut := runCapture([]string{"VALID_KEY_XYZ"}, "/usr/local/bin/fish")
	if code == 2 && strings.Contains(errOut, "invalid variable name") {
		t.Errorf("valid key should not fail S1 validation: %q", errOut)
	}
}
