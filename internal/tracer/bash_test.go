package tracer

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestBashTracerNonLogin verifies that bashTracer in non-login mode discovers
// a variable exported from .bashrc in a controlled temp HOME.
func TestBashTracerNonLogin(t *testing.T) {
	tr := BashTracer()
	if !tr.Available() {
		t.Skip("bash not found")
	}

	home := prepareBashHome(t, false)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)

	keys := map[string]struct{}{
		"WHERENV_BASH_TEST":       {},
		"WHERENV_BASH_LOGIN_TEST": {},
	}

	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error: %v", err)
	}

	t.Logf("Shell: %s", result.Shell)
	t.Logf("ShellVersion: %s", result.ShellVersion)
	t.Logf("SentinelSeen: %v", result.SentinelSeen)
	t.Logf("Events (%d):", len(result.Events))
	for _, ev := range result.Events {
		t.Logf("  [%d] %s  %s:%d (conf=%v) raw=%q", ev.Order, ev.Name, ev.File, ev.Line, ev.LineConf, ev.RawCode)
	}

	if !result.SentinelSeen {
		t.Error("sentinel not seen")
	}
	// .bashrc sets WHERENV_BASH_TEST; event must be present.
	// On bash 3.2 with a long temp path, the file may be truncated — accept any prefix.
	assertEventFoundFilePrefix(t, result.Events, "WHERENV_BASH_TEST", home)
	// login-only var must not appear in non-login.
	assertEventAbsent(t, result.Events, "WHERENV_BASH_LOGIN_TEST")

	// ADR-2: bash 3.2 events should be LineBestEffort or LineUnknown (not LineExact).
	_, major := bashVersionInfo()
	if major < 4 {
		for _, ev := range result.Events {
			if ev.LineConf == LineExact {
				t.Errorf("bash %d event %s has LineExact; expected LineBestEffort/LineUnknown", major, ev.Name)
			}
		}
		t.Logf("bash %d: all events confirmed at LineBestEffort or LineUnknown", major)
	}
}

// TestBashTracerLogin verifies that login mode sources .bash_profile.
func TestBashTracerLogin(t *testing.T) {
	tr := BashTracer()
	if !tr.Available() {
		t.Skip("bash not found")
	}

	home := prepareBashHome(t, true)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)

	keys := map[string]struct{}{
		"WHERENV_BASH_TEST":       {},
		"WHERENV_BASH_LOGIN_TEST": {},
	}

	result, err := tr.Trace(context.Background(), Login, keys, 0)
	if err != nil {
		t.Logf("Trace returned error: %v", err)
	}

	t.Logf("Shell: %s  Mode: Login  SentinelSeen: %v", result.Shell, result.SentinelSeen)
	for _, ev := range result.Events {
		t.Logf("  [%d] %s  %s:%d (conf=%v) raw=%q", ev.Order, ev.Name, ev.File, ev.Line, ev.LineConf, ev.RawCode)
	}

	if !result.SentinelSeen {
		t.Error("sentinel not seen")
	}
	// On bash 3.2 with long temp path, file may be truncated; accept any prefix match.
	assertEventFoundFilePrefix(t, result.Events, "WHERENV_BASH_LOGIN_TEST", home)
}

// TestBashTracerLongPathTruncation verifies that bash 3.2 truncation on long
// paths results in LineUnknown events (not silently dropped).
func TestBashTracerLongPathTruncation(t *testing.T) {
	tr := BashTracer()
	if !tr.Available() {
		t.Skip("bash not found")
	}
	_, major := bashVersionInfo()
	if major >= 4 {
		t.Skip("bash 4+ does not truncate PS4; skipping truncation test")
	}

	// Create a HOME path long enough to trigger 99-byte PS4 truncation.
	// We need: 1+38+len(home)+len("/.bashrc")+1+len(lineNum) > 99
	// With lineNum=1: 1+38+len(home)+8+1+1 = 49+len(home) > 99 → len(home) > 50
	base := t.TempDir()
	// Pad the home dir name to exceed 50 chars.
	longSuffix := strings.Repeat("a", 60)
	home := base + "/" + longSuffix
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, home+"/.bashrc", "export WHERENV_TRUNC_TEST=truncval\n")

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", oldHome)

	keys := map[string]struct{}{"WHERENV_TRUNC_TEST": {}}
	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error: %v", err)
	}

	t.Logf("SentinelSeen: %v  Events: %d", result.SentinelSeen, len(result.Events))
	for _, ev := range result.Events {
		t.Logf("  [%d] %s  %s:%d (conf=%v) raw=%q", ev.Order, ev.Name, ev.File, ev.Line, ev.LineConf, ev.RawCode)
	}

	// The event must be present with LineUnknown (truncated) and file best-effort.
	found := false
	for _, ev := range result.Events {
		if ev.Name == "WHERENV_TRUNC_TEST" {
			found = true
			if ev.LineConf != LineUnknown {
				t.Errorf("expected LineUnknown for truncated event, got %v", ev.LineConf)
			}
			if ev.File == "" {
				t.Error("expected non-empty File for truncated event")
			}
			if ev.RawCode == "" {
				t.Error("expected non-empty RawCode recovered via salvage")
			}
			t.Logf("PASS: truncated event recovered: file=%q rawCode=%q conf=%v", ev.File, ev.RawCode, ev.LineConf)
		}
	}
	if !found {
		t.Error("expected event WHERENV_TRUNC_TEST not found — salvage may have failed")
	}
}

// prepareBashHome creates a temp HOME with .bashrc and optionally .bash_profile.
func prepareBashHome(t *testing.T, withProfile bool) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir+"/.bashrc", "export WHERENV_BASH_TEST=bashrc\n")
	if withProfile {
		writeFile(t, dir+"/.bash_profile", "export WHERENV_BASH_LOGIN_TEST=profile\n")
	}
	return dir
}
