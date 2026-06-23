package tracer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// stderrLimit caps how many bytes of xtrace output we keep in memory (S4).
const stderrLimit = 64 * 1024 * 1024 // 64 MB

// defaultTimeout is the per-spawn wall-clock budget.
const defaultTimeout = 8 * time.Second

// spawnSpec describes a single shell invocation.
type spawnSpec struct {
	// argv is the full argument vector, e.g. ["zsh", "-ixc", "echo __WHERENV_END_<n>__"].
	// Callers must bake the nonce into argv (sentinel echo) and env (PS4) before passing.
	argv []string
	// env is the environment for the child process.
	env []string
	// nonce is the crypto/rand hex string that was baked into argv and env by the caller.
	// It is returned unchanged in traceOutput so the caller can pass it to parseTrace.
	nonce string
	// timeout overrides defaultTimeout when non-zero.
	timeout time.Duration
}

// traceOutput is the raw result of runTrace.
type traceOutput struct {
	stderr []byte
	nonce  string
}

// newNonce generates a 16-byte cryptographically random hex string (S3).
func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// runTrace spawns spec.argv[0] with the given argv, captures stderr, and
// returns the output together with the nonce embedded in the sentinel.
//
// Security guarantees (S3/S4):
//   - nonce comes from crypto/rand; callers must not fabricate it.
//   - Setsid puts the child in a new session (new process group, no controlling
//     terminal); on timeout the whole group is killed via Kill(-pgid, SIGKILL)
//     so child processes do not linger, and /dev/tty reads in startup fail fast
//     instead of blocking on the launching terminal.
//   - stdin is /dev/null.
//   - stdout is discarded.
//   - stderr is captured through an io.LimitReader (stderrLimit) to prevent
//     a runaway `set -x` from exhausting memory.
func runTrace(ctx context.Context, spec spawnSpec) (traceOutput, error) {
	if spec.nonce == "" {
		return traceOutput{}, fmt.Errorf("spawnSpec.nonce must be set by caller (use newNonce())")
	}
	nonce := spec.nonce

	timeout := spec.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	devNull, err := os.Open("/dev/null")
	if err != nil {
		return traceOutput{}, fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()

	// Use a pipe for stderr so we can enforce a size limit on the captured
	// xtrace output. Note: the read end is NOT used to detect completion (a
	// backgrounded descendant can hold the write end open); completion is
	// driven by cmd.Wait below.
	pr, pw, err := os.Pipe()
	if err != nil {
		return traceOutput{}, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Build the command manually (not exec.CommandContext) so we can attach
	// SysProcAttr.Setpgid before Start. exec.CommandContext only kills the
	// direct child on timeout, leaving grandchildren alive (S4 requirement).
	cmd := &exec.Cmd{
		Path:   resolveExecutable(spec.argv[0]),
		Args:   spec.argv,
		Env:    spec.env,
		Stdin: devNull,
		// Stdout MUST be an *os.File (not io.Discard): a non-file writer makes
		// os/exec create an internal pipe + copy goroutine that cmd.Wait blocks
		// on until EOF. A backgrounded descendant that inherits fd 1 would then
		// hang cmd.Wait for the lifetime of that process. Routing to /dev/null
		// directly passes the fd and avoids the goroutine entirely.
		Stdout: devNull,
		Stderr: pw,
		SysProcAttr: &syscall.SysProcAttr{
			// Setsid puts the child in a NEW SESSION with NO controlling terminal.
			// Two reasons:
			//   1. Whole-tree cleanup: the child is its own process-group leader
			//      (pgid == pid), so killGroup via Kill(-pid) reaps descendants (S4).
			//   2. No controlling TTY: a startup command that reads /dev/tty (e.g.
			//      a compinit insecure-dir prompt, or a tool's `read`) would block
			//      forever when wherenv is launched from a real terminal. With no
			//      controlling tty, /dev/tty access fails fast and startup proceeds
			//      instead of hanging until the timeout.
			Setsid: true,
		},
	}

	start := time.Now()
	dbg("spawn start: %v (timeout %s)", spec.argv, timeout)

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return traceOutput{}, fmt.Errorf("start %v: %w", spec.argv, err)
	}
	// Close the parent's copy of the write end; remaining holders are the child
	// and any descendants that inherited the fd.
	pw.Close()

	pid := cmd.Process.Pid

	// killGroup terminates the entire process group. Safe to call multiple times.
	killGroup := func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}

	// processExited is closed once cmd.Wait returns.
	processExited := make(chan struct{})

	// Watch the context: on timeout/cancel, kill the whole process group so a
	// genuinely hanging foreground shell is unblocked.
	go func() {
		select {
		case <-ctx.Done():
			killGroup()
		case <-processExited:
		}
	}()

	// Drain stderr in a goroutine, NOT the foreground. The pipe's write end is
	// inherited by every descendant of the shell, so a lingering BACKGROUND
	// process started during startup (ssh-agent, gpg-agent, a daemon, an `&`
	// job, an async-plugin worker) keeps the write end open even after the
	// foreground shell has exited. Blocking on pipe EOF here would stall until
	// the timeout fires. Instead we complete on the foreground shell's exit
	// (cmd.Wait) and then kill the group to release those lingering holders.
	var stderrBuf bytes.Buffer
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderrBuf, io.LimitReader(pr, stderrLimit))
		close(copyDone)
	}()

	// Wait for the foreground shell itself to exit. With both stdout and stderr
	// routed to real files (devNull / pw), cmd.Wait is a plain waitpid and
	// returns as soon as the shell exits — it does NOT wait for backgrounded
	// descendants. By this point the shell has written all of its xtrace output
	// (including the sentinel) to the pipe.
	waitErr := cmd.Wait()
	dbg("spawn: foreground exited after %s (waitErr=%v, ctxErr=%v)", time.Since(start), waitErr, ctx.Err())
	close(processExited) // stop the context watcher

	// Release any lingering background descendants still holding the stderr pipe
	// write end (S4 cleanup; in the common no-tty case they share the group and
	// this lets the drain goroutine see EOF).
	killGroup()

	// Collect the drained output. If an out-of-group orphan (a job-control
	// background process) still holds the stderr write end, the drain goroutine
	// will not see EOF; after a short grace period to absorb buffered bytes we
	// force the read end closed so we never block on the orphan's lifetime.
	graceTimer := time.NewTimer(250 * time.Millisecond)
	select {
	case <-copyDone:
		graceTimer.Stop()
		pr.Close()
	case <-graceTimer.C:
		pr.Close() // unblock io.Copy, which then closes copyDone
		<-copyDone
	}
	dbg("spawn: drain done after %s, captured %d bytes", time.Since(start), stderrBuf.Len())

	// A non-zero shell exit is expected (startup scripts may fail).
	// Only report a timeout as an error; callers still get the partial output.
	if ctx.Err() != nil {
		_ = waitErr
		return traceOutput{
			stderr: stderrBuf.Bytes(),
			nonce:  nonce,
		}, fmt.Errorf("spawn timed out after %s", timeout)
	}

	return traceOutput{
		stderr: stderrBuf.Bytes(),
		nonce:  nonce,
	}, nil
}

// resolveExecutable resolves a bare command name to a full path using PATH
// lookup, falling back to the name itself if LookPath fails.
func resolveExecutable(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}
