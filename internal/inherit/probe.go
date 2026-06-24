// Package inherit probes the origin of inherited environment variables using
// platform-specific mechanisms. On macOS, launchctl is the only supported
// probe (ADR-3: /etc/environment is inactive on macOS; ripgrep full-text
// search is deferred to a later version).
package inherit

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Probe reports whether name is present in the macOS launchd session
// environment (the global env store managed by launchctl).
//
// Security (the value never enters wherenv's address space):
// `launchctl getenv NAME` prints the variable's value to stdout — and its exit
// code is 0 whether or not the variable is set, so presence can only be told
// from whether any bytes were printed. Reading that stdout ourselves would pull
// the secret into wherenv's memory. Instead we wire launchctl's stdout straight
// into `wc -c` through an OS pipe (no shell involved, S5) and read back only the
// byte count. The value flows launchctl → kernel pipe → wc and is never copied
// into this process; a non-zero count means the variable is set in launchd.
//
// Any failure (binaries missing, pipe/exec error, unparseable count) is treated
// as "not present" so the caller falls back to the generic inherited message.
//
// The caller must have already validated name against the S1 regex.
func Probe(name string) bool {
	lcPath, err := exec.LookPath("launchctl")
	if err != nil {
		return false
	}
	wcPath, err := exec.LookPath("wc")
	if err != nil {
		return false
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return false
	}
	// Both ends are closed below once the children own their copies; the defers
	// are idempotent backstops in case an early return is taken.
	defer pr.Close()
	defer pw.Close()

	// launchctl writes the value into the pipe — never into a buffer we read (S5:
	// argv elements are separate strings, no shell).
	lc := exec.Command(lcPath, "getenv", name)
	lc.Stdout = pw

	// wc consumes the value from the pipe and emits only a byte count.
	wc := exec.Command(wcPath, "-c")
	wc.Stdin = pr
	var count bytes.Buffer
	wc.Stdout = &count

	if err := wc.Start(); err != nil {
		return false
	}
	if err := lc.Start(); err != nil {
		// Unblock wc (waiting on the pipe) before reaping it.
		pr.Close()
		pw.Close()
		_ = wc.Wait()
		return false
	}

	// Close our copies so wc observes EOF once launchctl exits. wc and launchctl
	// each hold their own dup'd descriptors.
	pw.Close()
	pr.Close()

	_ = lc.Wait()
	if err := wc.Wait(); err != nil {
		return false
	}

	n, err := strconv.Atoi(strings.TrimSpace(count.String()))
	if err != nil {
		return false
	}
	return n > 0
}
