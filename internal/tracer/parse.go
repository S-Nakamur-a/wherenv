package tracer

import (
	"regexp"
	"strconv"
	"strings"
)

// assignRE matches common shell assignment forms in a PS4 raw code line.
// Groups: 1=optional prefix keyword, 2=variable name, 3=operator (= or +=).
var assignRE = regexp.MustCompile(
	`^(?:export |typeset -x |declare -x )?([A-Za-z_][A-Za-z0-9_]*)(\+?=)`,
)

// exportValuelessRE matches a bare `export NAME` without `=`.
var exportValuelessRE = regexp.MustCompile(
	`^export ([A-Za-z_][A-Za-z0-9_]*)$`,
)

// parseTrace parses the stderr output of one shell spawn into a list of
// AssignEvent values and reports whether the startup sentinel was seen.
//
// Parameters:
//   - raw   — the full stderr string captured from the shell.
//   - keys  — set of variable names to keep (S2: filter by string equality, not regex).
//   - nonce — the crypto/rand hex nonce baked into the PS4 markers.
//
// The parse follows the plan's "パース手順" steps 1–8.
func parseTrace(raw string, keys map[string]struct{}, nonce string) (events []AssignEvent, sentinelSeen bool) {
	openTag := "<WE" + nonce + ">"
	closeTag := "<EOWE" + nonce + ">"
	sentinel := "__WHERENV_END_" + nonce + "__"

	// seen tracks (file, line, name) triples to deduplicate bash double-output (step 7).
	type key3 struct{ file string; line int; name string }
	seen := make(map[key3]struct{})

	order := 0

	for _, line := range strings.Split(raw, "\n") {
		// Step 8: sentinel line marks end of startup section.
		if strings.Contains(line, sentinel) {
			sentinelSeen = true
			break
		}

		// Step 1: strip leading `+` characters (xtrace nesting markers).
		stripped := strings.TrimLeft(line, "+")

		// Step 2: locate <WE<n>> — must appear at the row start (after '+' stripping)
		// per S3 design: the nonce marker is always the first token on a xtrace line.
		if !strings.HasPrefix(stripped, openTag) {
			// Not a xtrace line with our marker; skip.
			continue
		}
		afterOpen := stripped[len(openTag):]

		var file string
		var lineNum int
		var lineConf LineConfidence
		var rawCode string

		segment, tail, found := strings.Cut(afterOpen, closeTag)
		if !found {
			// Step 3: <EOWE> not found → truncation → LineUnknown.
			// Try nonce-aware salvage: locate the partial <EOWE prefix, extract
			// file:line from what precedes it, and recover rawCode from what
			// follows after skipping the truncated nonce tail.
			// If <EOWE is entirely absent, use key-based tail scan (ADR-2).
			file, lineNum, rawCode = salvageTruncated(afterOpen, nonce, keys)
			lineConf = LineUnknown
		} else {
			// Step 2: split the file:line segment on the last ':'.
			file, lineNum = splitFileLine(segment)
			lineConf = LineExact
			// Step 4: everything after <EOWE<n>> is the raw code.
			rawCode = strings.TrimSpace(tail)
		}

		// Skip non-truncated lines that have no code (e.g. bare command traces).
		if rawCode == "" && lineConf == LineExact {
			continue
		}

		// Step 5: extract variable name from rawCode. After this point we keep
		// only the name and the append flag — rawCode (which carries the value)
		// is never stored in the event, so the value is dropped immediately.
		name, isAppend := extractVarName(rawCode)
		if name == "" {
			continue
		}

		// Step 6: filter by keys (string equality, S2).
		if _, ok := keys[name]; !ok {
			continue
		}

		// Step 7: deduplicate bash double-output by (file, line, name).
		k3 := key3{file: file, line: lineNum, name: name}
		if _, dup := seen[k3]; dup {
			continue
		}
		seen[k3] = struct{}{}

		events = append(events, AssignEvent{
			Name:     name,
			File:     file,
			Line:     lineNum,
			LineConf: lineConf,
			Append:   isAppend,
			Order:    order,
		})
		order++
	}

	return events, sentinelSeen
}

// splitFileLine splits "file:line" on the last ':' and returns (file, lineNum).
// If there is no ':', the whole input is the file and lineNum is 0.
func splitFileLine(s string) (file string, lineNum int) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return s, 0
	}
	n, err := strconv.Atoi(s[idx+1:])
	if err != nil {
		// Could not parse line number; return the whole thing as file.
		return s, 0
	}
	return s[:idx], n
}

// salvageTruncated attempts to recover file, lineNum, and rawCode from a
// truncated PS4 line (bash 3.x cuts at PS4TruncLimit bytes).
//
// afterOpen is everything after the <WE<n>> open tag. Two truncation layouts:
//
//  1. Partial EOWE: "<file>:<line><EOWE<partial-nonce><rawcode>"
//     The file:line section overran PS4TruncLimit bytes and <EOWE cut mid-tag.
//     Strategy: find "<EOWE", split file:line there, skip partial nonce, rest=rawCode.
//
//  2. EOWE completely absent: "<file_truncated><rawcode>"
//     The file path itself hit PS4TruncLimit bytes before <EOWE could even start.
//     Strategy: for each key, scan the combined string for "<key>=" or "export <key>";
//     the position of the match separates the file part from the rawCode.
//
// keys is used only for case 2 (key-based tail scan per ADR-2).
func salvageTruncated(afterOpen, nonce string, keys map[string]struct{}) (file string, lineNum int, rawCode string) {
	const eowePfx = "<EOWE"
	idx := strings.Index(afterOpen, eowePfx)
	if idx >= 0 {
		// Case 1: partial EOWE present.
		file, lineNum = splitFileLine(afterOpen[:idx])

		// Advance past "<EOWE" then skip remaining nonce characters.
		pos := idx + len(eowePfx)
		for i := 0; i < len(nonce) && pos < len(afterOpen); i++ {
			if afterOpen[pos] != nonce[i] {
				break
			}
			pos++
		}
		// Skip '>' and ' ' if still present (they are absent when cut).
		if pos < len(afterOpen) && afterOpen[pos] == '>' {
			pos++
		}
		if pos < len(afterOpen) && afterOpen[pos] == ' ' {
			pos++
		}
		rawCode = afterOpen[pos:]
		return file, lineNum, rawCode
	}

	// Case 2: <EOWE entirely absent. Use key-based tail scan.
	// For each key in keys, search for the pattern at the boundary point
	// where the rawCode starts (appended directly after the truncated file path).
	//
	// Known limitation (D5): if the key name appears inside the file path itself
	// (e.g. "/home/user/FOO=dir/.zshrc" for key "FOO"), this scan will split at
	// the wrong position, producing a corrupted rawCode. No recovery is possible
	// without the full PS4 line. Such paths are extremely rare in practice.
	bestPos := -1
	for key := range keys {
		// Patterns to look for at the code boundary.
		patterns := []string{
			"export " + key + "=",
			"export " + key + " ",  // valueless export: "export KEY" + next token
			"export " + key + "\n",
			"export " + key,        // export at end of string
			key + "+=",
			key + "=",
		}
		for _, pat := range patterns {
			if p := strings.Index(afterOpen, pat); p >= 0 {
				if bestPos < 0 || p < bestPos {
					bestPos = p
				}
				break
			}
		}
	}

	if bestPos < 0 {
		// Could not locate rawCode boundary; return just whatever file we can glean.
		file, lineNum = splitFileLine(afterOpen)
		return file, lineNum, ""
	}

	fileSegment := afterOpen[:bestPos]
	// fileSegment is "<file>:<lineNum>" but lineNum may not be present if
	// the cut happened before the digit. Use best-effort split.
	file, lineNum = splitFileLine(fileSegment)
	rawCode = afterOpen[bestPos:]
	return file, lineNum, rawCode
}

// extractVarName returns the variable name and whether it is an append (+=)
// from a raw xtrace code line. Returns ("", false) if no assignment is found.
func extractVarName(rawCode string) (name string, isAppend bool) {
	// Try standard assignment forms: NAME=, export NAME=, typeset -x NAME=, etc.
	if m := assignRE.FindStringSubmatch(rawCode); m != nil {
		return m[1], strings.HasPrefix(m[2], "+")
	}
	// Try valueless export: "export NAME"
	if m := exportValuelessRE.FindStringSubmatch(rawCode); m != nil {
		return m[1], false
	}
	return "", false
}
