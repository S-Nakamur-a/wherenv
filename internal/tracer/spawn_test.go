package tracer

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSpawnTimeout verifies that:
//
//	(a) a spawn with a 1s timeout returns in roughly 1 second even when
//	    the shell would otherwise run for 100 seconds (sleep 100), and
//	(b) no residual zsh or sleep processes are left behind afterwards.
func TestSpawnTimeout(t *testing.T) {
	zshPath, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not found, skipping spawn test")
	}

	n, err := newNonce()
	if err != nil {
		t.Fatal(err)
	}
	spec := spawnSpec{
		argv:    []string{zshPath, "-ixc", "sleep 100; echo end"},
		nonce:   n,
		timeout: 1 * time.Second,
	}

	start := time.Now()
	out, err := runTrace(context.Background(), spec)
	elapsed := time.Since(start)

	t.Logf("runTrace returned after %s (expected ~1s timeout)", elapsed.Round(time.Millisecond))
	t.Logf("runTrace error (expected timeout): %v", err)
	t.Logf("stderr bytes captured: %d", len(out.stderr))

	// (a) Must return within a generous 3s window around the 1s timeout.
	if elapsed > 3*time.Second {
		t.Errorf("runTrace took %s, expected ~1s", elapsed)
	}

	// Give the OS a moment to reap processes.
	time.Sleep(500 * time.Millisecond)

	// (b) No residual "sleep 100" processes should remain.
	// Use pgrep -f to search for the command text.
	out2, _ := exec.Command("pgrep", "-fl", "sleep 100").Output()
	residual := strings.TrimSpace(string(out2))
	if residual != "" {
		t.Errorf("residual process(es) found after timeout:\n%s", residual)
	} else {
		t.Log("PASS: no residual 'sleep 100' processes")
	}
}

// TestKillGroup is a low-level check that syscall.Kill(-pgid, SIGKILL) works
// as expected on this platform.
func TestKillGroup(t *testing.T) {
	zshPath, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not found")
	}

	cmd := &exec.Cmd{
		Path: zshPath,
		Args: []string{zshPath, "-c", "sleep 999"},
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Logf("spawned pid=%d", pid)

	time.Sleep(100 * time.Millisecond)

	pgid, err := syscall.Getpgid(pid)
	t.Logf("pgid=%d, err=%v", pgid, err)

	// Kill the group.
	killErr := syscall.Kill(-pgid, syscall.SIGKILL)
	t.Logf("Kill(-pgid=%d, SIGKILL) err: %v", pgid, killErr)

	time.Sleep(200 * time.Millisecond)

	// Check that both zsh and sleep are gone.
	out, _ := exec.Command("pgrep", "-fl", "sleep 999").Output()
	residual := strings.TrimSpace(string(out))
	if residual != "" {
		t.Errorf("residual after group kill:\n%s", residual)
	} else {
		fmt.Println("PASS: group kill worked")
	}

	// Reap.
	_ = cmd.Wait()
}

// TestSpawnDoesNotWaitForBackgroundChild is a regression test for the bug where
// a process backgrounded during shell startup inherited runTrace's stdout/stderr
// fds and made the spawn block until the timeout: io.Discard on Stdout caused
// os/exec to spawn an internal copy goroutine that cmd.Wait waited on until the
// stdout pipe reached EOF, which a lingering background descendant prevented.
// runTrace must complete as soon as the FOREGROUND process exits.
func TestSpawnDoesNotWaitForBackgroundChild(t *testing.T) {
	nonce, err := newNonce()
	if err != nil {
		t.Fatal(err)
	}
	// The foreground shell starts a 30s sleeper (which inherits stdout+stderr)
	// and then exits immediately. runTrace must return in well under both the
	// sleeper's lifetime and the 10s timeout.
	spec := spawnSpec{
		argv:    []string{"sh", "-c", "(sleep 30 &) ; echo done >&2"},
		env:     []string{"PATH=/usr/bin:/bin"},
		nonce:   nonce,
		timeout: 10 * time.Second,
	}
	start := time.Now()
	out, err := runTrace(context.Background(), spec)
	elapsed := time.Since(start)

	// Clean up the lingering sleeper regardless of outcome.
	_ = exec.Command("pkill", "-f", "sleep 30").Run()

	if err != nil {
		t.Fatalf("runTrace returned error: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("runTrace took %v; it is blocking on the background child instead of the foreground exit", elapsed)
	}
	if !strings.Contains(string(out.stderr), "done") {
		t.Errorf("foreground stderr 'done' not captured, got %q", out.stderr)
	}
}
