// Package mise probes whether an environment variable was set by mise,
// using `mise env --json-extended` which returns per-variable provenance
// (the source config file) as JSON.
package mise

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"time"

	"github.com/S-Nakamur-a/wherenv/internal/report"
)

// miseExecTimeout is the maximum wall-clock time allowed for `mise env
// --json-extended` to complete. Consistent with the project's policy of
// bounding every exec call (tracer uses per-spawn timeout; launchctl completes
// near-instantly; direnv uses no exec). 5 s is generous for a read-only query.
const miseExecTimeout = 5 * time.Second

// maxMiseOutput is the maximum number of bytes read from mise's stdout.
// Mirrors direnv's zlibDecompressLimit policy: cap output from an external
// process to prevent excessive memory use from a misbehaving or adversarial
// mise binary. 10 MiB is well above any realistic mise env output.
const maxMiseOutput = 10 << 20 // 10 MiB

// Runner is a function that invokes `mise env --json-extended` and returns its
// stdout. The abstraction allows tests to inject a stub without spawning a real
// process (S5).
type Runner func() ([]byte, error)

// DefaultRunner locates the mise binary via exec.LookPath (graceful degrade
// when mise is not installed), then runs `mise env --json-extended` without
// involving a shell (S5). The cwd is inherited from the calling process so
// mise picks up the nearest mise.toml as it would in normal use.
//
// A context with miseExecTimeout bounds execution time. stdout is read through
// a LimitReader capped at maxMiseOutput bytes; exceeding the limit returns an
// error so Probe degrades gracefully. stderr is not captured or parsed.
func DefaultRunner() ([]byte, error) {
	misePath, err := exec.LookPath("mise")
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), miseExecTimeout)
	defer cancel()

	// No shell involvement: misePath and fixed args are separate argv elements (S5).
	cmd := exec.CommandContext(ctx, misePath, "env", "--json-extended")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Read at most maxMiseOutput+1 bytes so we can detect the overrun.
	limited := io.LimitReader(stdout, maxMiseOutput+1)
	out, readErr := io.ReadAll(limited)

	// Always wait for the process to exit; capture the wait error separately.
	waitErr := cmd.Wait()

	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	if int64(len(out)) > maxMiseOutput {
		return nil, io.ErrUnexpectedEOF // output exceeded limit
	}
	return out, nil
}

// miseEnvEntry is the per-variable JSON structure returned by
// `mise env --json-extended`. Source is present only for variables that were
// explicitly set in a mise config file (e.g. [env] in mise.toml); variables
// like PATH that mise mutates but does not own have an empty Source.
//
// Security: the "value" field is deliberately NOT decoded. We only need the
// source path for provenance; leaving Value out of the struct means the
// variable values in mise's JSON are discarded during unmarshalling rather than
// retained in wherenv.
type miseEnvEntry struct {
	Source string `json:"source"`
}

// Probe runs the provided Runner once and returns a map from variable name to
// ToolSource for every variable that mise set via a config file (i.e. entries
// with a non-empty "source" field). Variables managed by mise for other reasons
// (PATH, shims) have no "source" and are excluded — satisfying acceptance
// condition 4 ("PATH is not claimed").
//
// Probe never returns an error and never panics:
//   - Runner error (LookPath failure, non-zero exit, I/O failure, size exceeded) → empty map.
//   - json.Unmarshal failure → empty map.
//   - Entries without a source → silently skipped.
func Probe(run Runner) map[string]report.ToolSource {
	result := make(map[string]report.ToolSource)

	out, err := run()
	if err != nil {
		return result
	}

	var raw map[string]miseEnvEntry
	if err := json.Unmarshal(out, &raw); err != nil {
		return result
	}

	for name, entry := range raw {
		if entry.Source == "" {
			// No source: mise manages this variable (e.g. PATH) but did not set it
			// via [env]; skip it to avoid false positives.
			continue
		}
		result[name] = report.ToolSource{
			Tool: "mise",
			File: entry.Source,
		}
	}
	return result
}
