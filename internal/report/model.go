// Package report holds the output model and formatter for wherenv results.
package report

import "github.com/S-Nakamur-a/wherenv/internal/tracer"

// Origin describes how a variable ended up in the environment.
type Origin int

const (
	// Startup means the variable was set by a shell startup file.
	Startup Origin = iota
	// Inherited means the variable exists in env but was not set by any traced startup file.
	Inherited
	// Unset means the variable is not present in env and was not seen in any startup file.
	Unset
	// Toolset means the variable was set by a developer tool (e.g. direnv) and its
	// origin has been proven by inspecting tool-specific environment metadata.
	Toolset
)

// AssignmentSite is one location in a startup file where an assignment occurred.
// The same physical site (file+line) may appear in multiple shell modes; those
// are folded into a single AssignmentSite with Modes carrying both Mode values.
type AssignmentSite struct {
	File         string
	Line         int
	LineConf     tracer.LineConfidence
	RawCode      string
	Append       bool
	Modes        []tracer.Mode // modes in which this site was observed
	ShellVersion string        // version string from the shell that produced this site
}

// Verdict describes the winner (last effective assignment) for a Startup variable.
// When the variable appears in both login and non-login modes, both per-mode winners
// are recorded rather than attempting to pick one across modes (ADR-6).
type Verdict struct {
	// PerMode holds one winner per mode. Key is tracer.Mode.
	// Empty if no non-append assignment was found.
	PerMode map[tracer.Mode]*AssignmentSite
	// HasAppend is true if any += was used in the assignment chain.
	HasAppend bool
}

// ToolSource carries provenance information for a variable set by a developer
// tool (Origin == Toolset). Tool is the tool name (e.g. "direnv"), File is the
// configuration file that set the variable (e.g. the .envrc path), and Value is
// the raw variable value as recorded by the tool.
type ToolSource struct {
	Tool  string
	File  string
	Value string
}

// Finding is the top-level result for a single variable name.
type Finding struct {
	Name    string
	Origin  Origin
	Sites   []AssignmentSite // ordered by appearance; only populated for Startup
	Verdict Verdict          // only meaningful when Origin == Startup
	// InheritedSource is set by the inherit probe (Step 8); may be empty.
	InheritedSource string
	// ToolSource is set when Origin == Toolset; nil otherwise.
	ToolSource *ToolSource
	// SentinelMissing is true for any mode where SentinelSeen was false,
	// meaning the trace may be incomplete.
	SentinelMissing bool
}
