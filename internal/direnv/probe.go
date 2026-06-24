// Package direnv probes whether an environment variable was set by direnv,
// using the DIRENV_DIFF environment variable that direnv injects into the shell
// session to encode the diff between the pre- and post-.envrc environment.
package direnv

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/S-Nakamur-a/wherenv/internal/report"
)

// zlibDecompressLimit is the maximum number of bytes we will decompress from
// DIRENV_DIFF. This guards against malformed or adversarial payloads causing
// excessive memory allocation.
const zlibDecompressLimit = 10 * 1024 * 1024 // 10 MiB

// direnvDiff is the decoded JSON structure of the DIRENV_DIFF value.
// Only the "n" (next) field is needed: variables present in n were added or
// changed by direnv when it loaded the current .envrc.
//
// The map values (the variable values direnv recorded) are decoded because
// they are part of the DIRENV_DIFF JSON, but Probe only inspects key presence
// and never copies a value out — see Probe.
type direnvDiff struct {
	N map[string]string `json:"n"`
}

// Probe reports whether the variable named name was set by direnv in the
// current session. snap is the full environment snapshot (as returned by
// env.Snapshot). name must already have been validated by the S1 regex.
//
// The function decodes DIRENV_DIFF (zlib-compressed, base64url-encoded JSON)
// and checks whether name appears in the "n" (next) map. If it does, the
// variable was added or modified by direnv when it loaded the .envrc.
//
// Direnv internal keys (those starting with "DIRENV_") are always excluded to
// avoid false positives when the caller queries e.g. DIRENV_FILE itself.
//
// Any failure at any stage of decoding returns (ToolSource{}, false) so that
// the caller falls back to the Inherited classification — no crash, no panic.
func Probe(snap map[string]string, name string) (report.ToolSource, bool) {
	// Never claim direnv set its own internal metadata keys.
	if strings.HasPrefix(name, "DIRENV_") {
		return report.ToolSource{}, false
	}

	raw, ok := snap["DIRENV_DIFF"]
	if !ok || raw == "" {
		return report.ToolSource{}, false
	}

	diff, ok := decodeDiff(raw)
	if !ok {
		return report.ToolSource{}, false
	}

	// Presence-only check: we confirm direnv set this name but never read its
	// value out of the diff (the value is dropped with the rest of diff.N).
	if _, found := diff.N[name]; !found {
		return report.ToolSource{}, false
	}

	return report.ToolSource{
		Tool: "direnv",
		File: snap["DIRENV_FILE"],
	}, true
}

// decodeDiff performs the three-stage decode: base64url → zlib → JSON.
// It returns (diff, true) on success, or (zero, false) on any error.
func decodeDiff(encoded string) (direnvDiff, bool) {
	// Stage 1: base64url decode. direnv uses padded URLEncoding; fall back to
	// RawURLEncoding if that fails (some older builds omit padding).
	compressed, err := base64.URLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		compressed, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return direnvDiff{}, false
		}
	}

	// Stage 2: zlib decompress. Apply a size limit to prevent excessive memory
	// allocation from a malformed or adversarial DIRENV_DIFF value.
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return direnvDiff{}, false
	}
	defer r.Close()

	limited := io.LimitReader(r, zlibDecompressLimit)
	jsonBytes, err := io.ReadAll(limited)
	if err != nil {
		return direnvDiff{}, false
	}

	// Stage 3: JSON unmarshal. We only need the "n" field.
	var diff direnvDiff
	if err := json.Unmarshal(jsonBytes, &diff); err != nil {
		return direnvDiff{}, false
	}

	return diff, true
}
