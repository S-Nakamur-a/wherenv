package mise

import (
	"errors"
	"strings"
	"testing"
)

// ── Real fixtures (B: plan Step1 requires actual mise env --json-extended output) ──
//
// Both fixtures were captured verbatim from running `mise env --json-extended`
// in the scratchpad test directory:
//   /private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/
//   e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/
//
// fixtureNormal used mise.toml:  [env] MY_MISE_VAR = "hello-from-mise"
// fixtureEdgeCases used a temporarily extended mise.toml (see comment below).
// In both cases the full real PATH value is included and must be excluded by Probe.

// fixtureNormal is the verbatim output of `mise env --json-extended` with
// mise.toml containing only MY_MISE_VAR = "hello-from-mise".
// The full PATH value is real output; it must not appear in Probe's result.
const fixtureNormal = `{
  "MY_MISE_VAR": {
    "source": "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml",
    "value": "hello-from-mise"
  },
  "PATH": {
    "value": "/Users/shunnaka/.nodenv/shims:/Users/shunnaka/.progate/bin:/Users/shunnaka/.cargo/bin:/opt/homebrew/bin:/Users/shunnaka/.rye/shims:/System/Cryptexes/App/usr/bin:/bin:/usr/local/bin:/usr/local/go/bin:/Users/shunnaka/.nodenv/bin:/Users/shunnaka/.local/bin:/Users/shunnaka/go/bin:/usr/bin:/usr/sbin:/sbin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/local/bin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/bin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/appleinternal/bin:/pkg/env/global/bin:/Applications/Ghostty.app/Contents/MacOS:/Users/shunnaka/.orbstack/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/asana/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/code-review/unknown/bin:/Users/shunnaka/.claude/plugins/cache/conductor-marketplace/conductor/0.6.0/bin:/Users/shunnaka/.claude/plugins/cache/anthropic-agent-skills/document-skills/575462609294/bin:/Users/shunnaka/.claude/plugins/cache/anthropic-agent-skills/example-skills/575462609294/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/figma/2.2.60/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/frontend-design/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/gopls-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/greptile/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/php-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/playwright/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/plugin-dev/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/pr-review-toolkit/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/rust-analyzer-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/swift-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/twig-plugins/twig/0.3.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/typescript-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/agent-sdk-dev/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/code-review/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/feature-dev/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/frontend-design/1.1.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/hookify/0.1.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/pr-review-toolkit/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/ralph-wiggum/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/security-guidance/2.0.0/bin:/Users/shunnaka/.claude/plugins/cache/bento/bento/0.3.0/bin"
  }
}`

// fixtureEdgeCases is the verbatim output of `mise env --json-extended` with
// mise.toml temporarily containing:
//
//	[env]
//	MY_MISE_VAR   = "hello-from-mise"
//	SPACE_VAR     = "value with spaces"
//	TAB_VAR       = "value\twith\ttabs"   (literal tab characters)
//	EQUALS_VAR    = "key=value=more"
//	TRAILING_VAR  = "trailing space   "
//
// This is the actual JSON returned by mise 2026.6.12; the source paths are real.
// TAB_VAR shows mise outputs literal \t in the JSON string (JSON-escaped tab).
// The PATH value is the real full PATH from the machine; it has no "source" and
// must be excluded by Probe.
const fixtureEdgeCases = `{
  "EQUALS_VAR": {
    "source": "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml",
    "value": "key=value=more"
  },
  "MY_MISE_VAR": {
    "source": "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml",
    "value": "hello-from-mise"
  },
  "PATH": {
    "value": "/Users/shunnaka/.nodenv/shims:/Users/shunnaka/.progate/bin:/Users/shunnaka/.cargo/bin:/opt/homebrew/bin:/Users/shunnaka/.rye/shims:/System/Cryptexes/App/usr/bin:/bin:/usr/local/bin:/usr/local/go/bin:/Users/shunnaka/.nodenv/bin:/Users/shunnaka/.local/bin:/Users/shunnaka/go/bin:/usr/bin:/usr/sbin:/sbin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/local/bin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/bin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/appleinternal/bin:/pkg/env/global/bin:/Applications/Ghostty.app/Contents/MacOS:/Users/shunnaka/.orbstack/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/asana/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/code-review/unknown/bin:/Users/shunnaka/.claude/plugins/cache/conductor-marketplace/conductor/0.6.0/bin:/Users/shunnaka/.claude/plugins/cache/anthropic-agent-skills/document-skills/575462609294/bin:/Users/shunnaka/.claude/plugins/cache/anthropic-agent-skills/example-skills/575462609294/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/figma/2.2.60/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/frontend-design/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/gopls-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/greptile/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/php-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/playwright/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/plugin-dev/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/pr-review-toolkit/unknown/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/rust-analyzer-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/swift-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/twig-plugins/twig/0.3.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-plugins-official/typescript-lsp/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/agent-sdk-dev/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/code-review/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/feature-dev/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/frontend-design/1.1.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/hookify/0.1.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/pr-review-toolkit/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/ralph-wiggum/1.0.0/bin:/Users/shunnaka/.claude/plugins/cache/claude-code-plugins/security-guidance/2.0.0/bin:/Users/shunnaka/.claude/plugins/cache/bento/bento/0.3.0/bin"
  },
  "SPACE_VAR": {
    "source": "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml",
    "value": "value with spaces"
  },
  "TAB_VAR": {
    "source": "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml",
    "value": "value\twith\ttabs"
  },
  "TRAILING_VAR": {
    "source": "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml",
    "value": "trailing space   "
  }
}`

// realMiseTomlPath is the source path embedded in the above fixtures.
const realMiseTomlPath = "/private/tmp/claude-501/-Users-shunnaka-ghq-github-com-S-Nakamur-a-wherenv/e4cb4a52-5bec-4152-8893-795ebe27f382/scratchpad/misetest/mise.toml"

// ── (a) Normal fixture: source-bearing var is present in result ───────────────

// TestProbeNormalVarGolden tests against the verbatim real output of
// `mise env --json-extended`. This is the golden fixture test that would catch
// a change to mise's JSON key contract (source/value).
func TestProbeNormalVarGolden(t *testing.T) {
	calls := 0
	runner := func() ([]byte, error) {
		calls++
		return []byte(fixtureNormal), nil
	}

	got := Probe(runner)

	// (f) Runner must be called exactly once — not per-variable.
	if calls != 1 {
		t.Errorf("Runner call count: got %d, want 1", calls)
	}

	src, ok := got["MY_MISE_VAR"]
	if !ok {
		t.Fatal("expected MY_MISE_VAR in result map")
	}
	if src.Tool != "mise" {
		t.Errorf("Tool: got %q, want %q", src.Tool, "mise")
	}
	if src.File != realMiseTomlPath {
		t.Errorf("File: got %q, want %q", src.File, realMiseTomlPath)
	}
	if src.Value != "hello-from-mise" {
		t.Errorf("Value: got %q, want %q", src.Value, "hello-from-mise")
	}
}

// ── (b) PATH (source absent) must be excluded ─────────────────────────────────

func TestProbePathExcluded(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(fixtureNormal), nil
	}
	got := Probe(runner)
	if _, ok := got["PATH"]; ok {
		t.Error("PATH must not appear in result (no source field in fixture)")
	}
}

// ── (c) Edge-case values: spaces, tabs, '=', trailing spaces ─────────────────

// TestProbeEdgeCaseValues tests that Probe preserves all the tricky value forms
// losslessly. The fixture is verbatim real mise output.
func TestProbeEdgeCaseValues(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(fixtureEdgeCases), nil
	}
	got := Probe(runner)

	cases := []struct {
		name  string
		value string
	}{
		{"SPACE_VAR", "value with spaces"},
		{"TAB_VAR", "value\twith\ttabs"},
		{"EQUALS_VAR", "key=value=more"},
		{"TRAILING_VAR", "trailing space   "},
	}
	for _, tc := range cases {
		src, ok := got[tc.name]
		if !ok {
			t.Errorf("%s: missing from result map", tc.name)
			continue
		}
		if src.Value != tc.value {
			t.Errorf("%s Value: got %q, want %q", tc.name, src.Value, tc.value)
		}
	}
}

// TestProbeEdgeCaseValuesSourceFile verifies that the real source path is
// preserved for all edge-case variables.
func TestProbeEdgeCaseValuesSourceFile(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(fixtureEdgeCases), nil
	}
	got := Probe(runner)
	for _, name := range []string{"SPACE_VAR", "TAB_VAR", "EQUALS_VAR", "TRAILING_VAR"} {
		src, ok := got[name]
		if !ok {
			t.Errorf("%s: missing from result map", name)
			continue
		}
		if src.File != realMiseTomlPath {
			t.Errorf("%s File: got %q, want %q", name, src.File, realMiseTomlPath)
		}
		if src.Tool != "mise" {
			t.Errorf("%s Tool: got %q, want mise", name, src.Tool)
		}
	}
}

// ── (d) Runner error → empty non-nil map ──────────────────────────────────────

func TestProbeRunnerError(t *testing.T) {
	runner := func() ([]byte, error) {
		return nil, errors.New("mise not found")
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map on Runner error")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map on Runner error; got %v", got)
	}
}

// ── (e) Garbage JSON → empty non-nil map, no panic ───────────────────────────

func TestProbeGarbageJSON(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte("this is not json at all!!!"), nil
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map on JSON parse error")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map on garbage JSON; got %v", got)
	}
}

// ── (f) Runner call count == 1 ────────────────────────────────────────────────

func TestProbeRunnerCalledOnce(t *testing.T) {
	calls := 0
	runner := func() ([]byte, error) {
		calls++
		return []byte(fixtureEdgeCases), nil
	}
	Probe(runner)
	if calls != 1 {
		t.Errorf("Runner must be called exactly once; got %d call(s)", calls)
	}
}

// ── (A fix) Output size overrun → Probe returns empty map ────────────────────
//
// DefaultRunner caps stdout at maxMiseOutput bytes and returns an error on
// overrun. Probe must fold that error into an empty map. This is verified at
// the Probe level using a stub Runner that simulates the overrun response.

func TestProbeSizeOverrunReturnsEmptyMap(t *testing.T) {
	// Simulate a Runner that returns more bytes than maxMiseOutput.
	// We don't call DefaultRunner here (would be a live exec); instead we
	// verify that Probe correctly handles a Runner error that would arise from
	// the overrun path in DefaultRunner.
	runner := func() ([]byte, error) {
		// Return a payload that is larger than maxMiseOutput.
		huge := strings.Repeat("x", maxMiseOutput+1)
		// Wrap it in a JSON object so it's valid JSON — but the point is that
		// DefaultRunner would have returned an error before handing this to Probe.
		// Here we simulate the error path directly.
		_ = huge
		return nil, errors.New("output exceeded size limit")
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map on size-limit error")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map on overrun error; got %v", got)
	}
}

// ── (D) JSON type mismatches → empty map, no panic ───────────────────────────

// TestProbeJSONNull verifies that `null` (the top-level JSON null) does not
// panic and returns an empty non-nil map. json.Unmarshal into a
// map[string]miseEnvEntry with `null` succeeds but yields a nil map, so the
// for-range loop must be safe (it is: ranging over a nil map is a no-op in Go).
func TestProbeJSONNull(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`null`), nil
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map for JSON null input")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for JSON null; got %v", got)
	}
}

// TestProbeJSONArray verifies that a JSON array (type mismatch for the expected
// object) returns an empty non-nil map and does not panic.
func TestProbeJSONArray(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`["foo","bar"]`), nil
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map for JSON array input")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for JSON array; got %v", got)
	}
}

// TestProbeJSONNumber verifies that a bare JSON number returns an empty
// non-nil map without panicking.
func TestProbeJSONNumber(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`42`), nil
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map for JSON number input")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for JSON number; got %v", got)
	}
}

// ── (E) source=="" explicit exclusion ─────────────────────────────────────────

// TestProbeSourceEmptyStringExcluded verifies that an entry with an explicit
// source:"" (empty string, not a missing key) is excluded just like a missing
// source. This differs from the missing-key case: json.Unmarshal populates
// Source="" for both, so both must be rejected.
func TestProbeSourceEmptyStringExcluded(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`{"X":{"source":"","value":"v"}}`), nil
	}
	got := Probe(runner)
	if _, ok := got["X"]; ok {
		t.Error("entry with explicit source:\"\" must be excluded")
	}
}

// TestProbeSourcePresentValueEmpty verifies that an entry with a valid source
// but empty value is included — the variable exists in [env] with an empty
// value, which is a legitimate (if unusual) configuration.
func TestProbeSourcePresentValueEmpty(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`{"X":{"source":"/path/to/mise.toml","value":""}}`), nil
	}
	got := Probe(runner)
	src, ok := got["X"]
	if !ok {
		t.Fatal("entry with valid source but empty value must be included")
	}
	if src.Value != "" {
		t.Errorf("Value: got %q, want empty string", src.Value)
	}
	if src.File != "/path/to/mise.toml" {
		t.Errorf("File: got %q, want /path/to/mise.toml", src.File)
	}
}

// ── Misc edge cases ───────────────────────────────────────────────────────────

// TestProbeEmptyJSON verifies that an empty JSON object returns a non-nil
// empty map without panicking.
func TestProbeEmptyJSON(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`{}`), nil
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map for empty JSON object")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty JSON object; got %v", got)
	}
}

// TestProbeAllNoSource verifies that when all entries lack a source field the
// result is an empty non-nil map.
func TestProbeAllNoSource(t *testing.T) {
	runner := func() ([]byte, error) {
		return []byte(`{"PATH":{"value":"/usr/bin"},"MANPATH":{"value":"/usr/share/man"}}`), nil
	}
	got := Probe(runner)
	if got == nil {
		t.Fatal("Probe must return non-nil map")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map when no entries have a source; got %v", got)
	}
}
