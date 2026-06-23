package main

import (
	"strings"
	"testing"
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

func TestRunNoArgs(t *testing.T) {
	code, _, errOut := runCapture([]string{}, "/bin/zsh")
	if code != 2 {
		t.Errorf("no args: want exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "usage:") {
		t.Errorf("expected usage message; got %q", errOut)
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
	// Output should mention the variable is not set (Unset origin).
	if !strings.Contains(outStr, "not set") {
		t.Errorf("expected 'not set' in stdout for unset variable; got %q", outStr)
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

// ── Valid key with unsupported shell (smoke) ──────────────────────────────────

func TestRunValidKeyNoError(t *testing.T) {
	// A valid key name with unsupported shell should not return exit 2 due to validation.
	code, _, errOut := runCapture([]string{"VALID_KEY_XYZ"}, "/usr/local/bin/fish")
	if code == 2 && strings.Contains(errOut, "invalid variable name") {
		t.Errorf("valid key should not fail S1 validation: %q", errOut)
	}
}
