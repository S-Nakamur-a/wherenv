// Package classify turns raw TraceResult values into per-variable Finding reports.
package classify

import (
	"slices"

	"github.com/S-Nakamur-a/wherenv/internal/report"
	"github.com/S-Nakamur-a/wherenv/internal/tracer"
)

// Classify takes the complete set of trace results (typically two: one per mode
// for the user's shell) and the current env snapshot, and returns one Finding
// per requested key.
//
// Origin rules (ADR-5):
//   - Any assign event in any TraceResult → Startup.
//   - No events AND key in envSnap → Inherited.
//   - No events AND key not in envSnap → Unset.
//
// Site folding (ADR-1): if the same (file, line, name) appears in both login
// and non-login results, those are merged into a single AssignmentSite that
// carries both Mode values in its Modes slice.
//
// Verdict (ADR-6): per mode, the last assignment event in order (append or not)
// is the winner. We do not attempt to rank across modes; report shows
// per-mode winners.
func Classify(results []tracer.TraceResult, envSnap map[string]string, keys []string) []report.Finding {
	findings := make([]report.Finding, 0, len(keys))
	for _, key := range keys {
		findings = append(findings, classifyOne(key, results, envSnap))
	}
	return findings
}

// classifyOne classifies a single variable name.
func classifyOne(name string, results []tracer.TraceResult, envSnap map[string]string) report.Finding {
	// Collect all events for this name across all results, in result order.
	// We use a siteKey to fold same-(file,line,name) from different modes.
	type siteKey struct{ file string; line int }

	// siteMap preserves insertion order via siteOrder.
	siteMap := make(map[siteKey]*report.AssignmentSite)
	var siteOrder []siteKey

	// Per-mode last assignment event (append or non-append) for verdict.
	lastAssign := make(map[tracer.Mode]*report.AssignmentSite)
	hasAppend := false
	sentinelMissing := false

	for _, r := range results {
		if !r.SentinelSeen {
			sentinelMissing = true
		}
		for i := range r.Events {
			ev := &r.Events[i]
			if ev.Name != name {
				continue
			}

			sk := siteKey{file: ev.File, line: ev.Line}
			site, exists := siteMap[sk]
			if !exists {
				site = &report.AssignmentSite{
					File:         ev.File,
					Line:         ev.Line,
					LineConf:     ev.LineConf,
					RawCode:      ev.RawCode,
					Append:       ev.Append,
					Modes:        []tracer.Mode{r.Mode},
					ShellVersion: r.ShellVersion,
				}
				siteMap[sk] = site
				siteOrder = append(siteOrder, sk)
			} else {
				// Fold: add mode if not already present.
				if !slices.Contains(site.Modes, r.Mode) {
					site.Modes = append(site.Modes, r.Mode)
				}
				// If one mode shows the site as non-append and another as append,
				// prefer the non-append designation.
				if !ev.Append {
					site.Append = false
				}
			}

			if ev.Append {
				hasAppend = true
			}
			// Track the last assignment per mode for verdict (append or non-append).
			lastAssign[r.Mode] = site
		}
	}

	// Build ordered sites slice.
	sites := make([]report.AssignmentSite, 0, len(siteOrder))
	for _, sk := range siteOrder {
		sites = append(sites, *siteMap[sk])
	}

	if len(sites) == 0 {
		// No startup events.
		if _, inEnv := envSnap[name]; inEnv {
			return report.Finding{
				Name:            name,
				Origin:          report.Inherited,
				SentinelMissing: sentinelMissing,
			}
		}
		return report.Finding{
			Name:            name,
			Origin:          report.Unset,
			SentinelMissing: sentinelMissing,
		}
	}

	// Build per-mode verdict map.
	perMode := make(map[tracer.Mode]*report.AssignmentSite)
	for mode, site := range lastAssign {
		s := *site // copy to avoid aliasing
		perMode[mode] = &s
	}

	return report.Finding{
		Name:   name,
		Origin: report.Startup,
		Sites:  sites,
		Verdict: report.Verdict{
			PerMode:   perMode,
			HasAppend: hasAppend,
		},
		SentinelMissing: sentinelMissing,
	}
}
