package tracer

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
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

	// PS4 format: +<WV<n>>%N<WK<n>>${funcfiletrace[1]}<EOWV<n>><WE<n>>%x:%I<EOWE<n>><space>
	//   %x = current file, %I = line number in that file (S3).
	//   %N = name of the executing unit (function name, or file path at top level).
	//   ${funcfiletrace[1]} = "callerfile:callerline" of the enclosing function's
	//     call site — only expands when prompt_subst is on (enabled by the shim
	//     below); otherwise it stays literal and parseTrace discards it.
	// The leading via block lets parseTrace attribute function-mediated
	// assignments (e.g. envsource) to the conf.d line that called the helper,
	// not just to the helper's own source file.
	ps4 := `+<WV` + nonce + `>%N<WK` + nonce + `>${funcfiletrace[1]}<EOWV` + nonce + `><WE` + nonce + `>%x:%I<EOWE` + nonce + `> `

	// Use the real ZDOTDIR if set, otherwise fall back to $HOME.
	// This ensures we trace the user's actual dotfiles.
	zdotdir := os.Getenv("ZDOTDIR")
	if zdotdir == "" {
		zdotdir = os.Getenv("HOME")
	}

	// prompt_subst must be on while the startup files run so the PS4
	// ${funcfiletrace[1]} expands. zsh reads $ZDOTDIR/.zshenv first and always,
	// so we point ZDOTDIR at a throwaway shim whose .zshenv enables the option,
	// restores ZDOTDIR to the real directory (so .zprofile/.zshrc/.zlogin load
	// from there), and chains the user's real .zshenv. If the shim can't be
	// created we fall back to the real ZDOTDIR — the trace still works, only the
	// caller attribution is lost (the literal expansion is discarded).
	childZdotdir := zdotdir
	if shimDir, cleanup, err := makePromptSubstShim(zdotdir); err == nil {
		childZdotdir = shimDir
		defer cleanup()
	} else {
		dbg("zsh: prompt_subst shim unavailable (%v); caller attribution disabled", err)
	}

	// Build the child environment: start from the current process env,
	// then override ZDOTDIR and PS4.
	childEnv := overrideEnv(os.Environ(), map[string]string{
		"ZDOTDIR": childZdotdir,
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

// makePromptSubstShim creates a throwaway ZDOTDIR whose .zshenv turns on
// prompt_subst, restores ZDOTDIR to realZdotdir, and chains the user's real
// .zshenv. It returns the shim directory and a cleanup func that removes it.
//
// Security: realZdotdir is single-quote escaped before being written into the
// shim script, so a path containing shell metacharacters cannot break out of
// the quotes. No user-controlled variable name or value is involved (S2).
func makePromptSubstShim(realZdotdir string) (shimDir string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wherenv-zdot-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	real := shellSingleQuote(realZdotdir)
	content := "" +
		"setopt prompt_subst 2>/dev/null\n" +
		"export ZDOTDIR=" + real + "\n" +
		"[ -f " + real + "/.zshenv ] && source " + real + "/.zshenv\n"

	if err := os.WriteFile(filepath.Join(dir, ".zshenv"), []byte(content), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes so
// the result is a single safe shell word.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
