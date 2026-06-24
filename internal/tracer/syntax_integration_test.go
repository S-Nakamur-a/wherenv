package tracer

import (
	"context"
	"os"
	"strings"
	"testing"
)

// This file is the syntax integration suite: unlike parse_test.go (which feeds
// synthetic PS4 strings to parseTrace), every case here runs a REAL zsh against
// fixture dotfiles and asserts on the events that zshTracer.Trace actually
// produces. Its job is to ground our assumptions about the xtrace *surface form*
// of each assignment syntax — the thing parse_test.go cannot verify because it
// hardcodes what we believe zsh emits. The original bug (envsource's
// `export "$k=$v"` being traced as `export 'K=v'`) is exactly the kind of
// surface-form surprise this suite exists to catch.

// synthZshrcHeader is the .zshrc preamble shared by the fixtures: it bails out
// of non-interactive shells (matching a real interactive rc) and autoloads the
// envsource helper from the fixture's functions/ dir.
const synthZshrcHeader = "[[ ! -o interactive ]] && return\n" +
	"fpath=(\"${ZDOTDIR}/functions\" $fpath)\n" +
	"autoload -Uz envsource\n"

// zshSyntaxFixture lays out a ZDOTDIR exercising the assignment forms wherenv
// claims to support, and returns the dir plus the 1-based line number of the
// `envsource` call (needed to assert caller attribution).
func zshSyntaxFixture(t *testing.T) (dir string, envsourceCallLine int) {
	t.Helper()
	dir = t.TempDir()

	// The real envsource helper: reads a .env file as data and exports each line
	// via `export "$key=$value"` — the quoted-expansion form.
	if err := os.MkdirAll(dir+"/functions", 0o700); err != nil {
		t.Fatalf("mkdir functions: %v", err)
	}
	writeFile(t, dir+"/functions/envsource",
		"envsource() {\n"+
			"  local file=\"$1\" line key value\n"+
			"  while IFS= read -r line || [[ -n \"$line\" ]]; do\n"+
			"    [[ -z \"$line\" || \"$line\" == \\#* ]] && continue\n"+
			"    key=\"${line%%=*}\"; value=\"${line#*=}\"\n"+
			"    export \"$key=$value\"\n"+
			"  done < \"$file\"\n"+
			"}\n")
	if err := os.MkdirAll(dir+"/env", 0o700); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	writeFile(t, dir+"/env/test.env", "WHERENV_SYN_ENVSOURCE=fromenv\n")

	// Build .zshrc line by line so we can compute the envsource call line.
	lines := []string{
		"export WHERENV_SYN_PLAIN=1",            // plain export
		"typeset -gx WHERENV_SYN_GX=2",          // combined flag cluster
		"readonly -x WHERENV_SYN_RX=3",          // different builtin + -x
		"export -- WHERENV_SYN_DD=4",            // -- separator
		"WHERENV_SYN_BARE=5",                    // assign then…
		"export WHERENV_SYN_BARE",               // …valueless export
		"export WHERENV_SYN_APP=a",              // base for append (already exported)
		"WHERENV_SYN_APP+=b",                    // += append (zsh rejects `export FOO+=`)
		"typeset WHERENV_SYN_NOEXPORT=x",        // NON-export: must NOT be an event
		`envsource "${ZDOTDIR}/env/test.env"`,   // function-mediated → caller attribution
	}
	header := synthZshrcHeader
	headerLineCount := strings.Count(header, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "envsource ") {
			envsourceCallLine = headerLineCount + i + 1
		}
	}
	writeFile(t, dir+"/.zshrc", header+strings.Join(lines, "\n")+"\n")
	return dir, envsourceCallLine
}

func TestZshSyntaxForms(t *testing.T) {
	tr := ZshTracer()
	if !tr.Available() {
		t.Skip("zsh not found")
	}

	dir, envsourceCallLine := zshSyntaxFixture(t)
	t.Setenv("ZDOTDIR", dir)

	keys := mkKeys(
		"WHERENV_SYN_PLAIN", "WHERENV_SYN_GX", "WHERENV_SYN_RX", "WHERENV_SYN_DD",
		"WHERENV_SYN_BARE", "WHERENV_SYN_APP", "WHERENV_SYN_NOEXPORT",
		"WHERENV_SYN_ENVSOURCE",
	)

	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error (slow dotfiles?): %v", err)
	}
	if !result.SentinelSeen {
		t.Fatal("sentinel not seen — trace incomplete, cannot judge syntax coverage")
	}
	for _, ev := range result.Events {
		t.Logf("  %s  %s:%d (append=%v caller=%s:%d)", ev.Name, ev.File, ev.Line, ev.Append, ev.CallerFile, ev.CallerLine)
	}

	zshrc := dir + "/.zshrc"

	// Every supported export form must surface as an event in .zshrc itself.
	for _, name := range []string{
		"WHERENV_SYN_PLAIN", "WHERENV_SYN_GX", "WHERENV_SYN_RX",
		"WHERENV_SYN_DD", "WHERENV_SYN_BARE",
	} {
		assertEventFound(t, result.Events, name, zshrc)
		if ev := findEvent(result.Events, name); ev != nil && ev.CallerFile != "" {
			t.Errorf("%s: direct assignment must have no caller, got %s:%d", name, ev.CallerFile, ev.CallerLine)
		}
	}

	// += must be flagged as an append. WHERENV_SYN_APP has two events — the base
	// `export …=a` and the `…+=b` append — so scan for the appending one rather
	// than the first.
	if !hasAppendEvent(result.Events, "WHERENV_SYN_APP") {
		t.Error("WHERENV_SYN_APP: expected an Append=true event for the += form")
	}

	// A non-export `typeset NAME=` must NOT be claimed as an environment origin.
	assertEventAbsent(t, result.Events, "WHERENV_SYN_NOEXPORT")

	// envsource: the quoted `export 'NAME=val'` form must be found, attributed to
	// the helper file, with the caller pointing back at the envsource call line.
	if ev := findEvent(result.Events, "WHERENV_SYN_ENVSOURCE"); ev == nil {
		t.Fatal("WHERENV_SYN_ENVSOURCE: envsource-exported var not found (the original bug)")
	} else {
		if !strings.HasSuffix(ev.File, "/functions/envsource") {
			t.Errorf("WHERENV_SYN_ENVSOURCE: File=%q, want the envsource helper", ev.File)
		}
		if ev.CallerFile != zshrc {
			t.Errorf("WHERENV_SYN_ENVSOURCE: CallerFile=%q, want %q", ev.CallerFile, zshrc)
		}
		if ev.CallerLine != envsourceCallLine {
			t.Errorf("WHERENV_SYN_ENVSOURCE: CallerLine=%d, want %d (the envsource call)", ev.CallerLine, envsourceCallLine)
		}
	}
}

// TestZshColonAssignBlindSpot grounds the documented limitation that a value
// placed into the environment purely via `: ${NAME:=default}` expansion leaves
// no variable name in the xtrace stream: the only recoverable origin is a later
// explicit `export`. This pins the behavior so a future parser change that
// claims to "see" := is forced to update this test deliberately.
func TestZshColonAssignBlindSpot(t *testing.T) {
	tr := ZshTracer()
	if !tr.Available() {
		t.Skip("zsh not found")
	}

	dir := t.TempDir()
	// Only the `:=` default-assignment, no explicit export of the colon var.
	writeFile(t, dir+"/.zshrc",
		"[[ ! -o interactive ]] && return\n"+
			": ${WHERENV_SYN_COLON:=6}\n")
	t.Setenv("ZDOTDIR", dir)

	keys := mkKeys("WHERENV_SYN_COLON")
	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error: %v", err)
	}
	if !result.SentinelSeen {
		t.Fatal("sentinel not seen — trace incomplete")
	}
	// The name never appears as an assignment LHS in the trace, so no event.
	assertEventAbsent(t, result.Events, "WHERENV_SYN_COLON")
}

// findEvent returns the first event with the given name, or nil.
func findEvent(events []AssignEvent, name string) *AssignEvent {
	for i := range events {
		if events[i].Name == name {
			return &events[i]
		}
	}
	return nil
}

// hasAppendEvent reports whether any event for name is an append (+=).
func hasAppendEvent(events []AssignEvent, name string) bool {
	for i := range events {
		if events[i].Name == name && events[i].Append {
			return true
		}
	}
	return false
}
