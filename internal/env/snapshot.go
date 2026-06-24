// Package env provides environment variable utilities.
package env

import (
	"os"
	"strings"
)

// metaKeys is the allowlist of environment variables whose *value* wherenv
// actually needs to read — tool-provenance metadata consumed by the probes
// (e.g. direnv's DIRENV_DIFF/DIRENV_FILE). Every other variable is recorded for
// presence only.
var metaKeys = map[string]struct{}{
	"DIRENV_DIFF": {},
	"DIRENV_FILE": {},
}

// Snapshot returns the current process environment as a map, but deliberately
// drops the values of all variables except the metadata allowlist (metaKeys).
//
// Security: wherenv classifies variables by *presence* (whether a name is set),
// not by value. Retaining every value would keep a second full copy of the
// user's secrets alive for the whole run. Instead, non-metadata variables are
// stored with an empty value — the key is still present (so presence checks
// work) but the secret is never held. The raw "K=V" strings from os.Environ
// pass through the loop transiently; only allowlisted values are kept.
func Snapshot() map[string]string {
	m := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			// Malformed entry without '='; skip.
			continue
		}
		if _, keep := metaKeys[k]; keep {
			m[k] = v
		} else {
			// Presence only — drop the value so it is not retained.
			m[k] = ""
		}
	}
	return m
}

// Partition splits keys into those present in the env snapshot and those absent.
// present and absent each contain the keys from keys that are/aren't in snap.
func Partition(snap map[string]string, keys []string) (present, absent []string) {
	for _, k := range keys {
		if _, ok := snap[k]; ok {
			present = append(present, k)
		} else {
			absent = append(absent, k)
		}
	}
	return present, absent
}
