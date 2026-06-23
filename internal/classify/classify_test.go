package classify

import (
	"testing"

	"github.com/S-Nakamur-a/wherenv/internal/report"
	"github.com/S-Nakamur-a/wherenv/internal/tracer"
)

func makeEvent(name, file string, line int, rawCode string, append_ bool, order int) tracer.AssignEvent {
	return tracer.AssignEvent{
		Name:     name,
		File:     file,
		Line:     line,
		LineConf: tracer.LineExact,
		RawCode:  rawCode,
		Append:   append_,
		Order:    order,
	}
}

func makeResult(shell string, mode tracer.Mode, sentinel bool, events ...tracer.AssignEvent) tracer.TraceResult {
	return tracer.TraceResult{
		Shell:        shell,
		Mode:         mode,
		Events:       events,
		SentinelSeen: sentinel,
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		results []tracer.TraceResult
		envSnap map[string]string
		keys    []string
		// Assertions are per-finding.
		check func(t *testing.T, findings []report.Finding)
	}{
		{
			name: "same site in both modes - folded into one AssignmentSite with two Modes",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true,
					makeEvent("FOO", "/etc/zshrc", 5, "export FOO=x", false, 0)),
				makeResult("zsh", tracer.Login, true,
					makeEvent("FOO", "/etc/zshrc", 5, "export FOO=x", false, 0)),
			},
			envSnap: map[string]string{"FOO": "x"},
			keys:    []string{"FOO"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "FOO")
				if f.Origin != report.Startup {
					t.Fatalf("origin: got %v want Startup", f.Origin)
				}
				if len(f.Sites) != 1 {
					t.Fatalf("site count: got %d want 1", len(f.Sites))
				}
				if len(f.Sites[0].Modes) != 2 {
					t.Errorf("modes on folded site: got %v want [NonLogin Login]", f.Sites[0].Modes)
				}
			},
		},
		{
			name: "site only in non-login mode",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true,
					makeEvent("BAR", "/home/u/.zshrc", 10, "BAR=rc", false, 0)),
				makeResult("zsh", tracer.Login, true), // no events
			},
			envSnap: map[string]string{"BAR": "rc"},
			keys:    []string{"BAR"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "BAR")
				if f.Origin != report.Startup {
					t.Fatalf("origin: got %v want Startup", f.Origin)
				}
				if len(f.Sites) != 1 {
					t.Fatalf("site count: got %d want 1", len(f.Sites))
				}
				if len(f.Sites[0].Modes) != 1 || f.Sites[0].Modes[0] != tracer.NonLogin {
					t.Errorf("mode: got %v want [NonLogin]", f.Sites[0].Modes)
				}
			},
		},
		{
			name: "no events + env present = Inherited",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true),
				makeResult("zsh", tracer.Login, true),
			},
			envSnap: map[string]string{"SHLVL": "2"},
			keys:    []string{"SHLVL"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "SHLVL")
				if f.Origin != report.Inherited {
					t.Errorf("origin: got %v want Inherited", f.Origin)
				}
			},
		},
		{
			name: "no events + env absent = Unset",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true),
				makeResult("zsh", tracer.Login, true),
			},
			envSnap: map[string]string{},
			keys:    []string{"NOTEXIST"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "NOTEXIST")
				if f.Origin != report.Unset {
					t.Errorf("origin: got %v want Unset", f.Origin)
				}
			},
		},
		{
			name: "multiple assignments - winner is last assignment per mode",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true,
					makeEvent("PATH", "/etc/paths", 1, "PATH=/usr/bin", false, 0),
					makeEvent("PATH", "/etc/zshrc", 3, "PATH+=/extra", true, 1),
					makeEvent("PATH", "/home/u/.zshrc", 5, "PATH=/home/u/bin", false, 2),
				),
				makeResult("zsh", tracer.Login, true,
					makeEvent("PATH", "/etc/zprofile", 2, "PATH=/login", false, 0),
				),
			},
			envSnap: map[string]string{"PATH": "/home/u/bin"},
			keys:    []string{"PATH"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "PATH")
				if f.Origin != report.Startup {
					t.Fatalf("origin: got %v want Startup", f.Origin)
				}
				// Three distinct sites (different file+line) from non-login +
				// one from login = 4 total.
				if len(f.Sites) != 4 {
					t.Errorf("site count: got %d want 4", len(f.Sites))
				}
				// Append noted.
				if !f.Verdict.HasAppend {
					t.Error("HasAppend should be true")
				}
				// Non-login winner = /home/u/.zshrc (last assignment in that mode).
				winner := f.Verdict.PerMode[tracer.NonLogin]
				if winner == nil || winner.File != "/home/u/.zshrc" {
					t.Errorf("non-login winner: got %v want /home/u/.zshrc", winner)
				}
				// Login winner = /etc/zprofile.
				loginWinner := f.Verdict.PerMode[tracer.Login]
				if loginWinner == nil || loginWinner.File != "/etc/zprofile" {
					t.Errorf("login winner: got %v want /etc/zprofile", loginWinner)
				}
			},
		},
		{
			name: "sentinel missing is propagated",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, false, // SentinelSeen=false
					makeEvent("FOO", "/etc/zshrc", 1, "FOO=x", false, 0)),
			},
			envSnap: map[string]string{"FOO": "x"},
			keys:    []string{"FOO"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "FOO")
				if !f.SentinelMissing {
					t.Error("SentinelMissing should be true when any result lacks sentinel")
				}
			},
		},
		{
			// A1 regression: when the last assignment is +=, the winner must point
			// to that append site, not to the earlier non-append site.
			name: "winner is last assignment even if it is an append",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true,
					makeEvent("PATH", "/etc/zshrc", 1, "PATH=/base", false, 0),
					makeEvent("PATH", "/home/u/.zshrc", 5, "PATH+=/extra", true, 1),
				),
			},
			envSnap: map[string]string{"PATH": "/base:/extra"},
			keys:    []string{"PATH"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				f := findingFor(t, findings, "PATH")
				if f.Origin != report.Startup {
					t.Fatalf("origin: got %v want Startup", f.Origin)
				}
				winner := f.Verdict.PerMode[tracer.NonLogin]
				if winner == nil {
					t.Fatal("no non-login winner")
				}
				if winner.File != "/home/u/.zshrc" || winner.Line != 5 {
					t.Errorf("winner: got %s:%d want /home/u/.zshrc:5", winner.File, winner.Line)
				}
				if !winner.Append {
					t.Error("winner.Append should be true (last event was +=)")
				}
				if !f.Verdict.HasAppend {
					t.Error("HasAppend should be true")
				}
			},
		},
		{
			name: "multiple keys classified independently",
			results: []tracer.TraceResult{
				makeResult("zsh", tracer.NonLogin, true,
					makeEvent("A", "/f", 1, "A=1", false, 0)),
			},
			envSnap: map[string]string{"A": "1", "B": "2"},
			keys:    []string{"A", "B", "C"},
			check: func(t *testing.T, findings []report.Finding) {
				t.Helper()
				a := findingFor(t, findings, "A")
				b := findingFor(t, findings, "B")
				c := findingFor(t, findings, "C")
				if a.Origin != report.Startup {
					t.Errorf("A: want Startup got %v", a.Origin)
				}
				if b.Origin != report.Inherited {
					t.Errorf("B: want Inherited got %v", b.Origin)
				}
				if c.Origin != report.Unset {
					t.Errorf("C: want Unset got %v", c.Origin)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := Classify(tc.results, tc.envSnap, tc.keys)
			tc.check(t, findings)
		})
	}
}

func findingFor(t *testing.T, findings []report.Finding, name string) report.Finding {
	t.Helper()
	for _, f := range findings {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no finding for %q", name)
	return report.Finding{}
}
