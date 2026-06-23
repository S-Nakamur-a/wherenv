package tracer

import (
	"fmt"
	"os"
	"strings"
)

// debugEnabled turns on diagnostic logging when WHERENV_DEBUG is set in the
// environment. Logging goes to stderr and NEVER includes command text or values
// from the trace — only timings, counts, and file:line locations.
var debugEnabled = os.Getenv("WHERENV_DEBUG") != ""

func dbg(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "[wherenv-debug] "+format+"\n", args...)
	}
}

// lastTraceLocations extracts up to n trailing "file:line" locations from a raw
// xtrace capture. It returns ONLY the marker-delimited file:line segments (never
// the command text that follows), so it is safe to log even for secret-bearing
// startup files. Useful to see where a trace stalled before timing out.
func lastTraceLocations(raw, nonce string, n int) []string {
	openTag := "<WE" + nonce + ">"
	closeTag := "<EOWE" + nonce + ">"
	var locs []string
	for _, line := range strings.Split(raw, "\n") {
		i := strings.Index(line, openTag)
		if i < 0 {
			continue
		}
		rest := line[i+len(openTag):]
		j := strings.Index(rest, closeTag)
		if j < 0 {
			continue // truncated marker: skip to avoid leaking trailing command text
		}
		locs = append(locs, rest[:j])
	}
	if len(locs) > n {
		locs = locs[len(locs)-n:]
	}
	return locs
}
