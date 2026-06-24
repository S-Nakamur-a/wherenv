package tracer

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestZshTracerNonLogin verifies that zshTracer in non-login mode discovers
// a variable exported from .zshrc in a controlled temp ZDOTDIR.
func TestZshTracerNonLogin(t *testing.T) {
	tr := ZshTracer()
	if !tr.Available() {
		t.Skip("zsh not found")
	}

	zdotdir := prepareZdotdir(t)

	// Temporarily override ZDOTDIR in the process env so zshTracer picks it up.
	old := os.Getenv("ZDOTDIR")
	os.Setenv("ZDOTDIR", zdotdir)
	defer os.Setenv("ZDOTDIR", old)

	keys := map[string]struct{}{
		"WHERENV_TEST":        {},
		"WHERENV_TEST_LOGIN":  {},
		"WHERENV_TEST_SOURCED": {},
	}

	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error (may be timeout from slow dotfiles): %v", err)
	}

	t.Logf("Shell: %s", result.Shell)
	t.Logf("ShellVersion: %s", result.ShellVersion)
	t.Logf("SentinelSeen: %v", result.SentinelSeen)
	t.Logf("Events (%d):", len(result.Events))
	for _, ev := range result.Events {
		t.Logf("  [%d] %s  %s:%d (conf=%v append=%v)", ev.Order, ev.Name, ev.File, ev.Line, ev.LineConf, ev.Append)
	}

	// Assertions for non-login mode.
	if !result.SentinelSeen {
		t.Error("sentinel not seen in non-login trace")
	}
	assertEventFound(t, result.Events, "WHERENV_TEST", zdotdir+"/.zshrc")
	assertEventFound(t, result.Events, "WHERENV_TEST_SOURCED", zdotdir+"/extra.zsh")
	assertEventAbsent(t, result.Events, "WHERENV_TEST_LOGIN")
}

// TestZshTracerLogin verifies that login mode adds .zprofile assignments.
func TestZshTracerLogin(t *testing.T) {
	tr := ZshTracer()
	if !tr.Available() {
		t.Skip("zsh not found")
	}

	zdotdir := prepareZdotdir(t)

	old := os.Getenv("ZDOTDIR")
	os.Setenv("ZDOTDIR", zdotdir)
	defer os.Setenv("ZDOTDIR", old)

	keys := map[string]struct{}{
		"WHERENV_TEST":       {},
		"WHERENV_TEST_LOGIN": {},
	}

	result, err := tr.Trace(context.Background(), Login, keys, 0)
	if err != nil {
		t.Logf("Trace returned error: %v", err)
	}

	t.Logf("Shell: %s  Mode: Login  SentinelSeen: %v", result.Shell, result.SentinelSeen)
	for _, ev := range result.Events {
		t.Logf("  [%d] %s  %s:%d", ev.Order, ev.Name, ev.File, ev.Line)
	}

	if !result.SentinelSeen {
		t.Error("sentinel not seen in login trace")
	}
	assertEventFound(t, result.Events, "WHERENV_TEST", zdotdir+"/.zshrc")
	assertEventFound(t, result.Events, "WHERENV_TEST_LOGIN", zdotdir+"/.zprofile")
}

// prepareZdotdir creates a temporary ZDOTDIR with known dotfiles and returns its path.
func prepareZdotdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeFile(t, dir+"/.zshrc",
		"export WHERENV_TEST=rc\n"+
			"source "+dir+"/extra.zsh\n")
	writeFile(t, dir+"/extra.zsh",
		"export WHERENV_TEST_SOURCED=from_extra\n")
	writeFile(t, dir+"/.zprofile",
		"export WHERENV_TEST_LOGIN=prof\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// assertEventFound checks that at least one event for the given name exists
// and that its File matches the expected path.
func assertEventFound(t *testing.T, events []AssignEvent, name, wantFile string) {
	t.Helper()
	for _, ev := range events {
		if ev.Name == name {
			if ev.File != wantFile {
				t.Errorf("event %s: File=%q, want %q", name, ev.File, wantFile)
			}
			return
		}
	}
	t.Errorf("expected event for %q not found in %v", name, eventNames(events))
}

// assertEventAbsent checks that no event for the given name exists.
func assertEventAbsent(t *testing.T, events []AssignEvent, name string) {
	t.Helper()
	for _, ev := range events {
		if ev.Name == name {
			t.Errorf("unexpected event for %q found: %+v", name, ev)
		}
	}
}

func eventNames(events []AssignEvent) []string {
	names := make([]string, len(events))
	for i, ev := range events {
		names[i] = ev.Name
	}
	return names
}

// assertEventFoundFilePrefix checks that at least one event for name exists
// and that its File starts with the given prefix. This is used for bash 3.2
// where the file path may be truncated in the PS4 output.
func assertEventFoundFilePrefix(t *testing.T, events []AssignEvent, name, filePrefix string) {
	t.Helper()
	for _, ev := range events {
		if ev.Name == name {
			if !strings.HasPrefix(ev.File, filePrefix[:min(len(filePrefix), len(ev.File))]) {
				t.Errorf("event %s: File=%q does not start with prefix of %q", name, ev.File, filePrefix)
			}
			return
		}
	}
	t.Errorf("expected event for %q not found in %v", name, eventNames(events))
}

