package inherit

import (
	"os/exec"
	"testing"
)

// requireLaunchctl skips the test when launchctl or wc is unavailable (e.g. on
// non-macOS CI), since Probe depends on both.
func requireLaunchctl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("launchctl"); err != nil {
		t.Skip("launchctl not available")
	}
	if _, err := exec.LookPath("wc"); err != nil {
		t.Skip("wc not available")
	}
}

// TestProbeUnset verifies that a variable absent from the launchd session
// reports false (launchctl prints nothing → zero bytes → not present).
func TestProbeUnset(t *testing.T) {
	requireLaunchctl(t)
	if Probe("WHERENV_PROBE_DEFINITELY_UNSET_9z8y7x") {
		t.Error("expected false for a variable not set in the launchd session")
	}
}

// TestProbeSetThenUnset exercises the full presence detection against the real
// launchd session: a variable set via `launchctl setenv` must report true, and
// false again once unset. The value never enters wherenv (Probe pipes launchctl
// into wc and reads only the byte count); this test only checks the presence bit.
func TestProbeSetThenUnset(t *testing.T) {
	requireLaunchctl(t)

	const name = "WHERENV_PROBE_TEST_VAR"
	if err := exec.Command("launchctl", "setenv", name, "some-secret-value").Run(); err != nil {
		t.Skipf("could not set launchctl var (sandboxed?): %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("launchctl", "unsetenv", name).Run() })

	if !Probe(name) {
		t.Error("expected true for a variable present in the launchd session")
	}

	if err := exec.Command("launchctl", "unsetenv", name).Run(); err != nil {
		t.Fatalf("unsetenv failed: %v", err)
	}
	if Probe(name) {
		t.Error("expected false after the variable was unset")
	}
}

// TestProbeEmptyValueIsPresent documents that a variable set to the empty string
// is still reported as present: launchctl prints a trailing newline (1 byte),
// which is a legitimate "set in launchd" signal.
func TestProbeEmptyValueIsPresent(t *testing.T) {
	requireLaunchctl(t)

	const name = "WHERENV_PROBE_EMPTY_VAR"
	if err := exec.Command("launchctl", "setenv", name, "").Run(); err != nil {
		t.Skipf("could not set launchctl var (sandboxed?): %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("launchctl", "unsetenv", name).Run() })

	if !Probe(name) {
		t.Error("expected true for a variable set to the empty string in launchd")
	}
}
