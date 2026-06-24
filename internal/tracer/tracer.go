// Package tracer spawns a shell with xtrace enabled and parses the resulting
// PS4-annotated output to discover where environment variables are assigned.
package tracer

import (
	"context"
	"time"
)

// Mode distinguishes how the shell was invoked.
type Mode int

const (
	NonLogin Mode = iota
	Login
)

// LineConfidence describes how reliably the line number was recovered.
type LineConfidence int

const (
	// LineExact means the line number is reliable (modern bash or zsh %I).
	LineExact LineConfidence = iota
	// LineBestEffort means the number was recovered with caveats (e.g. bash 3.2 truncation).
	LineBestEffort
	// LineUnknown means the PS4 line was truncated before EOWE was seen.
	LineUnknown
)

// AssignEvent records one assignment trace line.
//
// Security: the assignment's right-hand side (the variable's value) is
// deliberately NOT retained. We parse the value out of the xtrace line only
// long enough to recover the variable name and the "+=" vs "=" operator, then
// discard it. wherenv reports WHERE a variable was set, never its value.
type AssignEvent struct {
	Name     string
	File     string
	Line     int
	LineConf LineConfidence
	Append   bool // true when "+=" was used
	Order    int  // 0-based position among events for this shell+mode run

	// CallerFile/CallerLine record where the enclosing function was *called*
	// from, when the assignment executed inside a function whose body lives in a
	// different file than the call site (e.g. an `envsource`-style loader defined
	// in ~/.config/zsh/functions but invoked from a conf.d file). For such
	// assignments File/Line point at the generic helper (the mechanism) while
	// CallerFile/CallerLine point at the line the user actually edits to change
	// the value. CallerFile is empty when the assignment ran directly in a
	// startup file (no indirection) or when the shell could not supply a caller
	// (zsh only; bash leaves these zero).
	CallerFile string
	CallerLine int
}

// TraceResult holds all events from one shell spawn (one shell, one mode).
type TraceResult struct {
	Shell        string
	Mode         Mode
	Events       []AssignEvent
	SentinelSeen bool
	ShellVersion string
}

// Tracer is the interface implemented by zshTracer and bashTracer.
type Tracer interface {
	Name() string
	Available() bool
	// Trace runs one shell spawn in the given mode and returns assignment events
	// for the requested keys. timeout sets the per-spawn wall-clock budget;
	// zero means use the implementation's default.
	Trace(ctx context.Context, mode Mode, keys map[string]struct{}, timeout time.Duration) (TraceResult, error)
}
