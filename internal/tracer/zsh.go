package tracer

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"strings"
	"time"
)

// zshTracer implements Tracer for zsh.
type zshTracer struct{}

// ZshTracer returns a Tracer that runs zsh startup traces.
func ZshTracer() Tracer { return zshTracer{} }

func (zshTracer) Name() string { return "zsh" }

// Available reports whether zsh is on PATH.
func (zshTracer) Available() bool {
	_, err := exec.LookPath("zsh")
	return err == nil
}

// Trace spawns zsh in the given mode (login / non-login) with xtrace enabled,
// captures the PS4-annotated stderr, and returns all assignment events for the
// requested keys. timeout sets the per-spawn wall-clock budget; zero uses the
// implementation default.
//
// Security: VARNAME values from keys are never interpolated into argv or env
// (S2). Filtering is done in parseTrace via string equality.
func (zshTracer) Trace(ctx context.Context, mode Mode, keys map[string]struct{}, timeout time.Duration) (TraceResult, error) {
	nonce, err := newNonce()
	if err != nil {
		return TraceResult{}, err
	}

	sentinel := "__WHERENV_END_" + nonce + "__"

	// PS4 format: +<WE<n>>%x:%I<EOWE<n>><space>
	// %x = current file being executed, %I = line number in that file (S3).
	ps4 := `+<WE` + nonce + `>%x:%I<EOWE` + nonce + `> `

	// Use the real ZDOTDIR if set, otherwise fall back to $HOME.
	// This ensures we trace the user's actual dotfiles.
	zdotdir := os.Getenv("ZDOTDIR")
	if zdotdir == "" {
		zdotdir = os.Getenv("HOME")
	}

	// Build the child environment: start from the current process env,
	// then override ZDOTDIR and PS4.
	childEnv := overrideEnv(os.Environ(), map[string]string{
		"ZDOTDIR": zdotdir,
		"PS4":     ps4,
	})

	// Build argv.
	flags := "-ixc"
	if mode == Login {
		flags = "-lixc"
	}
	zshPath := resolveExecutable("zsh")
	argv := []string{zshPath, flags, "echo " + sentinel}

	out, runErr := runTrace(ctx, spawnSpec{
		argv:    argv,
		env:     childEnv,
		nonce:   nonce,
		timeout: timeout,
	})

	// A timeout is non-fatal for parsing; we use whatever stderr we captured.
	// Other errors (exec failure) are fatal.
	if runErr != nil && out.stderr == nil {
		return TraceResult{}, runErr
	}

	events, sentinelSeen := parseTrace(string(out.stderr), keys, nonce)

	modeStr := "non-login"
	if mode == Login {
		modeStr = "login"
	}
	dbg("zsh %s: ZDOTDIR=%s, %d trace bytes, sentinelSeen=%v, %d matching events",
		modeStr, zdotdir, len(out.stderr), sentinelSeen, len(events))
	if debugEnabled && !sentinelSeen {
		dbg("zsh %s: trace INCOMPLETE — last file:line locations executed before the stall:", modeStr)
		for _, loc := range lastTraceLocations(string(out.stderr), nonce, 8) {
			dbg("    …%s", loc)
		}
	}

	// Stamp Order globally across all events.
	for i := range events {
		events[i].Order = i
	}

	ver := zshVersion()

	result := TraceResult{
		Shell:        "zsh",
		Mode:         mode,
		Events:       events,
		SentinelSeen: sentinelSeen,
		ShellVersion: ver,
	}

	// Surface timeout as a secondary error but still return partial results.
	return result, runErr
}

// zshVersion returns the output of `zsh --version`, trimmed.
func zshVersion() string {
	out, err := exec.Command(resolveExecutable("zsh"), "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// overrideEnv returns a copy of base with the given key=value pairs applied.
// If a key already exists in base it is replaced; otherwise it is appended.
func overrideEnv(base []string, overrides map[string]string) []string {
	// Build a set of keys we need to override; clone so we can delete as we go.
	remaining := maps.Clone(overrides)

	result := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			result = append(result, kv)
			continue
		}
		if val, found := remaining[k]; found {
			result = append(result, k+"="+val)
			delete(remaining, k)
		} else {
			result = append(result, kv)
		}
	}
	// Append any overrides not found in base.
	for k, v := range remaining {
		result = append(result, k+"="+v)
	}
	return result
}
