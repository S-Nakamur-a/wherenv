// Package inherit probes the origin of inherited environment variables using
// platform-specific mechanisms. On macOS, launchctl is the only supported
// probe (ADR-3: /etc/environment is inactive on macOS; ripgrep full-text
// search is deferred to a later version).
package inherit

import (
	"os/exec"
	"strings"
)

// Probe looks up name in the launchctl environment (macOS global env store).
// It returns the launchctl value if found, or "" if not set there.
//
// Security (S5): name is passed as a separate argument to exec.Command, never
// interpolated into a shell string. The caller must have already validated name
// against the S1 regex.
func Probe(name string) string {
	// exec.Command takes argv as distinct strings — no shell involved (S5).
	out, err := exec.Command("launchctl", "getenv", name).Output()
	if err != nil {
		// launchctl exits non-zero when the variable is not set; treat as absent.
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
