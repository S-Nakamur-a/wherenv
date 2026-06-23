package direnv

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// makeDiff encodes a map as a DIRENV_DIFF value using the same codec direnv
// uses: JSON → zlib compress → base64 URLEncoding (padded). This lets tests
// generate round-trip fixtures without needing direnv installed.
func makeDiff(t *testing.T, prev, next map[string]string) string {
	t.Helper()
	payload := map[string]map[string]string{"p": prev, "n": next}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("makeDiff: json.Marshal: %v", err)
	}

	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(jsonBytes); err != nil {
		t.Fatalf("makeDiff: zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("makeDiff: zlib close: %v", err)
	}

	return base64.URLEncoding.EncodeToString(buf.Bytes())
}

// makeDiffRaw is like makeDiff but encodes with RawURLEncoding (no padding),
// simulating older direnv builds that omit base64 padding.
func makeDiffRaw(t *testing.T, prev, next map[string]string) string {
	t.Helper()
	payload := map[string]map[string]string{"p": prev, "n": next}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("makeDiffRaw: json.Marshal: %v", err)
	}
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(jsonBytes); err != nil {
		t.Fatalf("makeDiffRaw: zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("makeDiffRaw: zlib close: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

// ── table-driven tests covering cases (a)–(g) from the plan ──────────────────

func TestProbe(t *testing.T) {
	// (a) variable present in n → true + correct File/Value
	t.Run("a_var_in_n_returns_true", func(t *testing.T) {
		diff := makeDiff(t, nil, map[string]string{"MY_VAR": "hello"})
		snap := map[string]string{
			"DIRENV_DIFF": diff,
			"DIRENV_FILE": "/home/user/project/.envrc",
			"MY_VAR":      "hello",
		}
		src, ok := Probe(snap, "MY_VAR")
		if !ok {
			t.Fatal("expected Probe to return true for a variable present in n")
		}
		if src.Tool != "direnv" {
			t.Errorf("Tool: got %q, want %q", src.Tool, "direnv")
		}
		if src.File != "/home/user/project/.envrc" {
			t.Errorf("File: got %q, want %q", src.File, "/home/user/project/.envrc")
		}
		if src.Value != "hello" {
			t.Errorf("Value: got %q, want %q", src.Value, "hello")
		}
	})

	// (b) DIRENV_ prefix key → false (internal keys excluded)
	t.Run("b_direnv_prefix_excluded", func(t *testing.T) {
		diff := makeDiff(t, nil, map[string]string{"DIRENV_FILE": "/some/.envrc"})
		snap := map[string]string{
			"DIRENV_DIFF": diff,
			"DIRENV_FILE": "/some/.envrc",
		}
		_, ok := Probe(snap, "DIRENV_FILE")
		if ok {
			t.Error("expected Probe to return false for a DIRENV_ prefixed key")
		}
	})

	// (c) name not in n → false
	t.Run("c_name_not_in_n_returns_false", func(t *testing.T) {
		diff := makeDiff(t, nil, map[string]string{"OTHER_VAR": "value"})
		snap := map[string]string{
			"DIRENV_DIFF": diff,
			"DIRENV_FILE": "/some/.envrc",
		}
		_, ok := Probe(snap, "MY_VAR")
		if ok {
			t.Error("expected Probe to return false when name is absent from n")
		}
	})

	// (d) garbage DIRENV_DIFF → false (graceful degrade, no crash)
	t.Run("d_garbage_direnv_diff_returns_false", func(t *testing.T) {
		snap := map[string]string{
			"DIRENV_DIFF": "this-is-not-valid-base64url-zlib-json!!!",
			"DIRENV_FILE": "/some/.envrc",
		}
		_, ok := Probe(snap, "MY_VAR")
		if ok {
			t.Error("expected Probe to return false for garbage DIRENV_DIFF")
		}
	})

	// (e) DIRENV_DIFF absent → false
	t.Run("e_no_direnv_diff_returns_false", func(t *testing.T) {
		snap := map[string]string{
			"MY_VAR": "hello",
		}
		_, ok := Probe(snap, "MY_VAR")
		if ok {
			t.Error("expected Probe to return false when DIRENV_DIFF is absent")
		}
	})

	// (f) DIRENV_FILE absent → true & File == ""
	t.Run("f_no_direnv_file_returns_true_with_empty_file", func(t *testing.T) {
		diff := makeDiff(t, nil, map[string]string{"MY_VAR": "hello"})
		snap := map[string]string{
			"DIRENV_DIFF": diff,
			// DIRENV_FILE intentionally omitted
			"MY_VAR": "hello",
		}
		src, ok := Probe(snap, "MY_VAR")
		if !ok {
			t.Fatal("expected Probe to return true even without DIRENV_FILE")
		}
		if src.File != "" {
			t.Errorf("File: got %q, want empty string when DIRENV_FILE is not set", src.File)
		}
	})

	// (h) RawURLEncoding fallback: padded URLEncoding fails, RawURLEncoding succeeds.
	// Older direnv builds omit the trailing '=' padding from the base64url value.
	// probe.go falls back to RawURLEncoding when the padded decode fails.
	t.Run("h_raw_url_encoding_fallback_returns_true", func(t *testing.T) {
		diff := makeDiffRaw(t, nil, map[string]string{"MY_VAR": "world"})
		snap := map[string]string{
			"DIRENV_DIFF": diff,
			"DIRENV_FILE": "/home/user/.envrc",
		}
		src, ok := Probe(snap, "MY_VAR")
		if !ok {
			t.Fatal("expected Probe to return true when DIRENV_DIFF uses RawURLEncoding (no padding)")
		}
		if src.Value != "world" {
			t.Errorf("Value: got %q, want %q", src.Value, "world")
		}
	})

	// (g-over) oversized decompressed payload → false, no crash.
	//
	// The LimitReader caps decompression at zlibDecompressLimit bytes. When the
	// payload exceeds that limit, io.ReadAll returns only the first
	// zlibDecompressLimit bytes, which truncates the JSON. json.Unmarshal then
	// fails on the truncated text → Probe returns false. The failure path is
	// "truncated JSON → unmarshal error → graceful false", not a memory guard
	// per se (no OOM is possible because LimitReader stops the read). The
	// important invariant: no panic, no hang, and Probe returns false.
	t.Run("g_over_limit_returns_false", func(t *testing.T) {
		// Build a JSON blob that is just over the 10 MiB limit.
		// Value of ~10.5 MiB ensures the JSON exceeds zlibDecompressLimit after decompression.
		large := strings.Repeat("a", zlibDecompressLimit+1024*512)
		payload := map[string]any{
			"p": map[string]string{},
			"n": map[string]string{"X": large},
		}
		jsonBytes, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("json.Marshal large payload: %v", err)
		}
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(jsonBytes); err != nil {
			t.Fatalf("zlib write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("zlib close: %v", err)
		}
		encoded := base64.URLEncoding.EncodeToString(buf.Bytes())
		snap := map[string]string{
			"DIRENV_DIFF": encoded,
			"DIRENV_FILE": "/some/.envrc",
		}
		// Must return false (JSON truncated at limit) without panicking or hanging.
		_, ok := Probe(snap, "X")
		if ok {
			t.Error("expected Probe to return false for oversized decompressed payload")
		}
	})

	// (g-under) payload just under the limit → true (boundary: limit is not
	// hit, JSON is intact, variable in n is found).
	t.Run("g_under_limit_returns_true", func(t *testing.T) {
		// Use a value of ~512 KiB — well within the 10 MiB limit but large enough
		// to exercise the LimitReader path without triggering truncation.
		value := strings.Repeat("b", 512*1024)
		diff := makeDiff(t, nil, map[string]string{"BIG_VAR": value})
		snap := map[string]string{
			"DIRENV_DIFF": diff,
			"DIRENV_FILE": "/some/.envrc",
		}
		src, ok := Probe(snap, "BIG_VAR")
		if !ok {
			t.Fatal("expected Probe to return true for a payload under the size limit")
		}
		if src.Value != value {
			t.Errorf("Value length: got %d, want %d", len(src.Value), len(value))
		}
	})
}

// ── Additional decode edge-cases ──────────────────────────────────────────────

// TestProbeNullAndEmptyN verifies that {"n":null} and {"n":{}} do not panic
// and return false (the variable name is not in an empty/null map).
func TestProbeNullAndEmptyN(t *testing.T) {
	makeEncoded := func(t *testing.T, jsonPayload string) string {
		t.Helper()
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write([]byte(jsonPayload)); err != nil {
			t.Fatalf("zlib write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("zlib close: %v", err)
		}
		return base64.URLEncoding.EncodeToString(buf.Bytes())
	}

	cases := []struct {
		name    string
		payload string
	}{
		{"n_null", `{"p":{},"n":null}`},
		{"n_empty", `{"p":{},"n":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := map[string]string{
				"DIRENV_DIFF": makeEncoded(t, tc.payload),
				"DIRENV_FILE": "/some/.envrc",
			}
			// Must not panic and must return false (MY_VAR not in null/empty n).
			_, ok := Probe(snap, "MY_VAR")
			if ok {
				t.Errorf("%s: expected false, got true", tc.name)
			}
		})
	}
}

// TestProbeValidBase64InvalidZlib verifies that a payload which decodes from
// base64 successfully but is not valid zlib returns false (zlib stage fails).
func TestProbeValidBase64InvalidZlib(t *testing.T) {
	// Encode arbitrary non-zlib bytes in base64url — valid base64, invalid zlib.
	notZlib := []byte("this is not zlib compressed data at all")
	encoded := base64.URLEncoding.EncodeToString(notZlib)
	snap := map[string]string{
		"DIRENV_DIFF": encoded,
		"DIRENV_FILE": "/some/.envrc",
	}
	_, ok := Probe(snap, "MY_VAR")
	if ok {
		t.Error("expected Probe to return false for valid base64 but invalid zlib payload")
	}
}
