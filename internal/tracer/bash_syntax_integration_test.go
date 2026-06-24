package tracer

import (
	"context"
	"strings"
	"testing"
)

// Bash counterpart to syntax_integration_test.go: run a REAL bash against a
// fixture .bashrc and assert on the events zshTracer's sibling bashTracer
// actually produces for each assignment syntax. Like the zsh suite, the point is
// to ground the xtrace *surface form* of bash's assignment builtins against the
// shared parser — not to re-test parse.go's synthetic cases.
//
// Assertions are name/flag based (not exact file:line) because macOS ships bash
// 3.2, whose PS4 expansion truncates at 99 bytes: with a long temp-HOME path the
// file is cut and the line is unknown. Caller attribution (columns 8/9) is a
// zsh-only feature, so bash events carry no caller and none is asserted here.

// bashSyntaxRC is the .bashrc body exercising bash's basic export forms. It
// avoids bash-4.2-only spellings (e.g. `declare -gx`) so the same fixture is
// meaningful on the stock macOS bash 3.2.
const bashSyntaxRC = `export WHERENV_BSYN_PLAIN=1
declare -x WHERENV_BSYN_DX=2
export WHERENV_BSYN_DD=4
WHERENV_BSYN_BARE=5
export WHERENV_BSYN_BARE
export WHERENV_BSYN_APP=a
export WHERENV_BSYN_APP+=b
declare WHERENV_BSYN_NOEXPORT=x
bash_envsource() {
  local file="$1" line key value
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    key="${line%%=*}"; value="${line#*=}"
    export "$key=$value"
  done < "$file"
}
bash_envsource "$HOME/test.env"
`

func TestBashSyntaxForms(t *testing.T) {
	tr := BashTracer()
	if !tr.Available() {
		t.Skip("bash not found")
	}

	home := t.TempDir()
	writeFile(t, home+"/.bashrc", bashSyntaxRC)
	writeFile(t, home+"/test.env", "WHERENV_BSYN_ENVSOURCE=fromenv\n")
	t.Setenv("HOME", home)

	keys := mkKeys(
		"WHERENV_BSYN_PLAIN", "WHERENV_BSYN_DX", "WHERENV_BSYN_DD",
		"WHERENV_BSYN_BARE", "WHERENV_BSYN_APP", "WHERENV_BSYN_NOEXPORT",
		"WHERENV_BSYN_ENVSOURCE",
	)

	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error (slow dotfiles?): %v", err)
	}
	if !result.SentinelSeen {
		t.Fatal("sentinel not seen — trace incomplete, cannot judge syntax coverage")
	}
	for _, ev := range result.Events {
		t.Logf("  %s  %s:%d (append=%v conf=%v)", ev.Name, ev.File, ev.Line, ev.Append, ev.LineConf)
	}

	// Every supported export form must surface as an event (name-based: the file
	// path may be truncated on bash 3.2).
	for _, name := range []string{
		"WHERENV_BSYN_PLAIN", "WHERENV_BSYN_DX", "WHERENV_BSYN_DD", "WHERENV_BSYN_BARE",
	} {
		if findEvent(result.Events, name) == nil {
			t.Errorf("%s: expected an event, none found", name)
		}
	}

	// `export FOO+=b` is valid in bash (unlike zsh) and must be flagged append.
	if !hasAppendEvent(result.Events, "WHERENV_BSYN_APP") {
		t.Error("WHERENV_BSYN_APP: expected an Append=true event for the += form")
	}

	// A non-export `declare NAME=` must NOT be claimed as an environment origin.
	assertEventAbsent(t, result.Events, "WHERENV_BSYN_NOEXPORT")

	// envsource-style helper: bash also quotes the word as `export 'NAME=val'`,
	// so the quoted-export form must be recovered here too.
	if findEvent(result.Events, "WHERENV_BSYN_ENVSOURCE") == nil {
		t.Error("WHERENV_BSYN_ENVSOURCE: quoted `export \"$k=$v\"` not recovered in bash")
	}
}

// TestBashQuotedExportSurface grounds the specific surface form at the heart of
// the original bug: bash must emit the quoted `export 'NAME=value'` for an
// `export "$k=$v"` so the parser's quote handling has a real producer to match.
func TestBashQuotedExportSurface(t *testing.T) {
	tr := BashTracer()
	if !tr.Available() {
		t.Skip("bash not found")
	}

	home := t.TempDir()
	writeFile(t, home+"/.bashrc",
		"k=WHERENV_BSYN_Q\n"+
			"v=somevalue\n"+
			"export \"$k=$v\"\n")
	t.Setenv("HOME", home)

	keys := mkKeys("WHERENV_BSYN_Q")
	result, err := tr.Trace(context.Background(), NonLogin, keys, 0)
	if err != nil {
		t.Logf("Trace returned error: %v", err)
	}
	if !result.SentinelSeen {
		t.Fatal("sentinel not seen — trace incomplete")
	}
	if findEvent(result.Events, "WHERENV_BSYN_Q") == nil {
		names := make([]string, 0, len(result.Events))
		for _, ev := range result.Events {
			names = append(names, ev.Name)
		}
		t.Errorf("WHERENV_BSYN_Q: quoted export not recovered; events=%s", strings.Join(names, ","))
	}
}
