package tracer

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PS4TruncLimit is the byte limit at which bash 3.x truncates PS4 expansions.
// Lines that exceed this length have their <EOWE> marker cut off or absent,
// triggering the salvageTruncated path in parseTrace.
const PS4TruncLimit = 99

// bashTracer implements Tracer for bash.
type bashTracer struct{}

// BashTracer returns a Tracer that runs bash startup traces.
func BashTracer() Tracer { return bashTracer{} }

func (bashTracer) Name() string { return "bash" }

// Available reports whether bash is on PATH.
func (bashTracer) Available() bool {
	_, err := exec.LookPath("bash")
	return err == nil
}

// Trace spawns bash in the given mode with xtrace enabled, captures the
// PS4-annotated stderr, and returns all assignment events for the requested keys.
// timeout sets the per-spawn wall-clock budget; zero uses the implementation default.
//
// bash 3.2 (the version shipped with macOS) truncates PS4 expansions at 99
// bytes. When that happens, parseTrace's salvageTruncated recovers file and
// rawCode; the LineConf on those events is LineUnknown.
//
// Security: VARNAME values from keys are never interpolated into argv or env (S2).
func (bashTracer) Trace(ctx context.Context, mode Mode, keys map[string]struct{}, timeout time.Duration) (TraceResult, error) {
	nonce, err := newNonce()
	if err != nil {
		return TraceResult{}, err
	}

	sentinel := "__WHERENV_END_" + nonce + "__"

	// PS4 format: +<WE<n>>${BASH_SOURCE}:${LINENO}<EOWE<n>><space>
	// The shell expands ${BASH_SOURCE} and ${LINENO} at trace time.
	ps4 := `+<WE` + nonce + `>${BASH_SOURCE}:${LINENO}<EOWE` + nonce + `> `

	home := os.Getenv("HOME")

	// Build the child environment: current env with HOME and PS4 overridden.
	childEnv := overrideEnv(os.Environ(), map[string]string{
		"HOME": home,
		"PS4":  ps4,
	})

	flags := "-ixc"
	if mode == Login {
		flags = "-lixc"
	}
	bashPath := resolveExecutable("bash")
	argv := []string{bashPath, flags, "echo " + sentinel}

	out, runErr := runTrace(ctx, spawnSpec{
		argv:    argv,
		env:     childEnv,
		nonce:   nonce,
		timeout: timeout,
	})

	if runErr != nil && out.stderr == nil {
		return TraceResult{}, runErr
	}

	events, sentinelSeen := parseTrace(string(out.stderr), keys, nonce)

	ver, majorVer := bashVersionInfo()

	// ADR-2: bash 3.2 reports LINENO=0 inside functions and has other precision
	// limitations. Downgrade all LineExact events to LineBestEffort for bash < 4.
	if majorVer < 4 {
		for i := range events {
			if events[i].LineConf == LineExact {
				events[i].LineConf = LineBestEffort
			}
		}
	}

	// Stamp Order globally.
	for i := range events {
		events[i].Order = i
	}

	result := TraceResult{
		Shell:        "bash",
		Mode:         mode,
		Events:       events,
		SentinelSeen: sentinelSeen,
		ShellVersion: ver,
	}

	return result, runErr
}

// bashVersionInfo returns the version string and major version number of bash.
func bashVersionInfo() (versionStr string, major int) {
	out, err := exec.Command(resolveExecutable("bash"), "--version").Output()
	if err != nil {
		return "", 0
	}
	// First line looks like: "GNU bash, version 3.2.57(1)-release ..."
	line := strings.SplitN(string(out), "\n", 2)[0]
	versionStr = strings.TrimSpace(line)

	// Extract major version number from "version X.Y.Z..."
	_, rest, ok := strings.Cut(line, "version ")
	if !ok {
		return versionStr, 0
	}
	majorStr, _, ok := strings.Cut(rest, ".")
	if !ok {
		return versionStr, 0
	}
	major, _ = strconv.Atoi(majorStr)
	return versionStr, major
}
