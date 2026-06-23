// Package env provides environment variable utilities.
package env

import (
	"os"
	"strings"
)

// Snapshot returns the current process environment as a map.
func Snapshot() map[string]string {
	m := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			// Malformed entry without '='; skip.
			continue
		}
		m[k] = v
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
