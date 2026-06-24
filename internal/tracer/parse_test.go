package tracer

import (
	"testing"
)

const testNonce = "deadbeefcafe0123deadbeefcafe0123"

// keys returns a map[string]struct{} from the given list of names.
func mkKeys(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// buildLine builds a realistic xtrace line in the format our PS4 produces.
// plusCount is the number of leading '+' characters (nesting level).
func buildLine(plusCount int, file string, lineNum string, rawCode string, nonce string) string {
	plus := ""
	for i := 0; i < plusCount; i++ {
		plus += "+"
	}
	return plus + "<WE" + nonce + ">" + file + ":" + lineNum + "<EOWE" + nonce + "> " + rawCode
}

// buildTruncatedLine builds a line where <EOWE> is absent (truncated by bash 3.2 or limit).
func buildTruncatedLine(plusCount int, partial string, nonce string) string {
	plus := ""
	for i := 0; i < plusCount; i++ {
		plus += "+"
	}
	return plus + "<WE" + nonce + ">" + partial
}

// buildBash32TruncatedLine simulates a real bash 3.2 truncated PS4 line.
// The PS4 is cut at 99 bytes (counted from the very start of the output line),
// leaving a partial <EOWE nonce, and the rawCode immediately follows.
//
// totalLineLimit is the PS4 cut-off in bytes from line start (99 for bash 3.2).
func buildBash32TruncatedLine(file, lineNum, rawCode, nonce string, totalLineLimit int) string {
	// Full PS4 content before truncation: +<WE<n>><file>:<lineNum><EOWE<n>>
	full := "+" + "<WE" + nonce + ">" + file + ":" + lineNum + "<EOWE" + nonce + "> "
	// Take the first totalLineLimit bytes of the PS4, then append rawCode.
	ps4Part := full
	if len(full) > totalLineLimit {
		ps4Part = full[:totalLineLimit]
	}
	return ps4Part + rawCode
}

func TestParseTrace(t *testing.T) {
	n := testNonce
	sentinel := "__WHERENV_END_" + n + "__"

	cases := []struct {
		name         string
		raw          string
		keys         map[string]struct{}
		wantEvents   []AssignEvent
		wantSentinel bool
	}{
		{
			name: "normal assignment",
			raw:  buildLine(1, "/etc/zshrc", "42", "FOO=bar", n),
			keys: mkKeys("FOO"),
			wantEvents: []AssignEvent{
				{Name: "FOO", File: "/etc/zshrc", Line: 42, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "export assignment",
			raw:  buildLine(1, "/etc/profile", "10", "export PATH=/usr/bin", n),
			keys: mkKeys("PATH"),
			wantEvents: []AssignEvent{
				{Name: "PATH", File: "/etc/profile", Line: 10, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "append operator",
			raw:  buildLine(1, "/home/user/.zshrc", "7", "PATH+=/extra/bin", n),
			keys: mkKeys("PATH"),
			wantEvents: []AssignEvent{
				{Name: "PATH", File: "/home/user/.zshrc", Line: 7, LineConf: LineExact, Append: true, Order: 0},
			},
		},
		{
			name: "EOWE absent - LineUnknown",
			// No <EOWE> tag: truncated line
			raw:  buildTruncatedLine(1, "/very/long/path/to/file.zsh:99", n),
			keys: mkKeys("FOO"),
			// No assignment in the truncated portion; no events expected because
			// rawCode is empty when truncated (we see no variable assignment text).
			wantEvents: nil,
		},
		{
			name: "EOWE absent with code smashed into file segment - key-based salvage",
			// The file path itself is so long that even <EOWE never starts.
			// The rawCode (" FOO=hello") is smashed into the afterOpen string.
			// The key-based tail scan finds "FOO=" and recovers rawCode.
			raw:  buildTruncatedLine(1, "/etc/zshrc:15 FOO=hello", n),
			keys: mkKeys("FOO"),
			wantEvents: []AssignEvent{
				// File will be "/etc/zshrc:15 " (whatever precedes "FOO="), Line=0
				// because the "file" segment has no parseable trailing :<digit>.
				{Name: "FOO", File: "/etc/zshrc:15 ", Line: 0, LineConf: LineUnknown, Append: false, Order: 0},
			},
		},
		{
			// Realistic bash 3.2 truncation: PS4 cut at 99 bytes, partial <EOWE,
			// rawCode immediately follows the cut. salvageTruncated must recover both.
			// Use a long file path (47+ chars) to ensure the EOWE tag gets cut.
			name: "bash 3.2 truncation - salvage file and rawCode",
			raw:  buildBash32TruncatedLine("/a/b/c/d/e/f/g/home/user/.bashrc_very_long_path", "7", "export WHERENV_LONG=x", n, 99),
			keys: mkKeys("WHERENV_LONG"),
			wantEvents: []AssignEvent{
				{Name: "WHERENV_LONG", File: "/a/b/c/d/e/f/g/home/user/.bashrc_very_long_path", Line: 7, LineConf: LineUnknown, Append: false, Order: 0},
			},
		},
		{
			// Bash 3.2 double-output: truncated line followed by its non-export twin.
			// Both share (file, line, name) so only one event should appear.
			name: "bash 3.2 truncation double-output dedup",
			raw: buildBash32TruncatedLine("/a/b/c/d/e/f/g/home/user/.bashrc_very_long_path", "7", "export WHERENV_LONG=x", n, 99) + "\n" +
				buildBash32TruncatedLine("/a/b/c/d/e/f/g/home/user/.bashrc_very_long_path", "7", "WHERENV_LONG=x", n, 99),
			keys: mkKeys("WHERENV_LONG"),
			wantEvents: []AssignEvent{
				{Name: "WHERENV_LONG", File: "/a/b/c/d/e/f/g/home/user/.bashrc_very_long_path", Line: 7, LineConf: LineUnknown, Append: false, Order: 0},
			},
		},
		{
			name: "key not in keys set - filtered out",
			raw:  buildLine(1, "/etc/zshrc", "5", "SECRET=hunter2", n),
			keys: mkKeys("OTHER"),
			wantEvents: nil,
		},
		{
			name: "bash double export dedup - export FOO=x appears twice",
			raw: buildLine(1, "/etc/profile", "3", "export FOO=x", n) + "\n" +
				buildLine(1, "/etc/profile", "3", "export FOO=x", n),
			keys: mkKeys("FOO"),
			// Step 7: same (file, line, name) → only one event.
			wantEvents: []AssignEvent{
				{Name: "FOO", File: "/etc/profile", Line: 3, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "sentinel stops parsing",
			raw: buildLine(1, "/etc/zshrc", "1", "FOO=before", n) + "\n" +
				sentinel + "\n" +
				buildLine(1, "/etc/zshrc", "2", "FOO=after", n),
			keys:         mkKeys("FOO"),
			wantSentinel: true,
			wantEvents: []AssignEvent{
				{Name: "FOO", File: "/etc/zshrc", Line: 1, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "sentinel absent - EOF treated as startup end",
			raw: buildLine(1, "/etc/zshrc", "5", "BAR=baz", n) + "\n" +
				buildLine(1, "/etc/zshrc", "6", "QUX=qux", n),
			keys:         mkKeys("BAR", "QUX"),
			wantSentinel: false,
			wantEvents: []AssignEvent{
				{Name: "BAR", File: "/etc/zshrc", Line: 5, LineConf: LineExact, Append: false, Order: 0},
				{Name: "QUX", File: "/etc/zshrc", Line: 6, LineConf: LineExact, Append: false, Order: 1},
			},
		},
		{
			name: "nested plus prefix (depth 3)",
			raw:  buildLine(3, "/usr/local/etc/zshrc", "100", "MYVAR=1", n),
			keys: mkKeys("MYVAR"),
			wantEvents: []AssignEvent{
				{Name: "MYVAR", File: "/usr/local/etc/zshrc", Line: 100, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "typeset -x form",
			raw:  buildLine(1, "/etc/zsh/zprofile", "20", "typeset -x GOPATH=/go", n),
			keys: mkKeys("GOPATH"),
			wantEvents: []AssignEvent{
				{Name: "GOPATH", File: "/etc/zsh/zprofile", Line: 20, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "declare -x form",
			raw:  buildLine(1, "/etc/bash.bashrc", "8", "declare -x HOME=/root", n),
			keys: mkKeys("HOME"),
			wantEvents: []AssignEvent{
				{Name: "HOME", File: "/etc/bash.bashrc", Line: 8, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "export valueless form",
			raw:  buildLine(1, "/etc/zshrc", "33", "export MYVAR", n),
			keys: mkKeys("MYVAR"),
			wantEvents: []AssignEvent{
				{Name: "MYVAR", File: "/etc/zshrc", Line: 33, LineConf: LineExact, Append: false, Order: 0},
			},
		},
		{
			name: "line without our marker is ignored",
			raw:  "+/etc/zshrc:5 FOO=bar",
			keys: mkKeys("FOO"),
			wantEvents: nil,
		},
		{
			// D5: known limitation — key-name-in-path false-split.
			// If the key name appears in the file path (e.g. key "FOO" and path
			// "/home/user/FOO=dir/.zshrc"), case-2 salvage splits at the wrong
			// boundary. The salvage finds "FOO=" at position 11 (inside the path
			// component "/home/user/FOO=dir/"), so fileSegment="/home/user/" and
			// rawCode="FOO=dir/.zshrc:10 FOO=realval" — the real assignment text is
			// mangled. This is a documented known limitation.
			// The test is a regression fence: verify no panic and event count is 1
			// with a wrong (but non-empty) rawCode starting with "FOO=".
			name: "D5 known-limitation: key name in directory path causes false split",
			raw:  buildTruncatedLine(1, "/home/user/FOO=dir/.zshrc:10 FOO=realval", n),
			keys: mkKeys("FOO"),
			// salvage produces 1 event with wrong rawCode ("FOO=dir/...") not "FOO=realval".
			wantEvents: []AssignEvent{
				{Name: "FOO", File: "/home/user/", Line: 0, LineConf: LineUnknown, Append: false, Order: 0},
			},
		},
		{
			name: "multiple different vars in order",
			raw: buildLine(1, "/etc/zshrc", "1", "A=1", n) + "\n" +
				buildLine(1, "/etc/zshrc", "2", "B=2", n) + "\n" +
				buildLine(1, "/etc/zshrc", "3", "A=3", n),
			keys: mkKeys("A", "B"),
			wantEvents: []AssignEvent{
				{Name: "A", File: "/etc/zshrc", Line: 1, LineConf: LineExact, Append: false, Order: 0},
				{Name: "B", File: "/etc/zshrc", Line: 2, LineConf: LineExact, Append: false, Order: 1},
				// Same name A but different line, so NOT a duplicate.
				{Name: "A", File: "/etc/zshrc", Line: 3, LineConf: LineExact, Append: false, Order: 2},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotEvents, gotSentinel := parseTrace(tc.raw, tc.keys, n)

			if gotSentinel != tc.wantSentinel {
				t.Errorf("sentinelSeen: got %v, want %v", gotSentinel, tc.wantSentinel)
			}

			if len(gotEvents) != len(tc.wantEvents) {
				t.Errorf("event count: got %d, want %d\ngot:  %+v\nwant: %+v",
					len(gotEvents), len(tc.wantEvents), gotEvents, tc.wantEvents)
				return
			}

			for i, got := range gotEvents {
				want := tc.wantEvents[i]
				if got.Name != want.Name {
					t.Errorf("[%d] Name: got %q want %q", i, got.Name, want.Name)
				}
				if got.File != want.File {
					t.Errorf("[%d] File: got %q want %q", i, got.File, want.File)
				}
				if got.Line != want.Line {
					t.Errorf("[%d] Line: got %d want %d", i, got.Line, want.Line)
				}
				if got.LineConf != want.LineConf {
					t.Errorf("[%d] LineConf: got %v want %v", i, got.LineConf, want.LineConf)
				}
				if got.Append != want.Append {
					t.Errorf("[%d] Append: got %v want %v", i, got.Append, want.Append)
				}
				if got.Order != want.Order {
					t.Errorf("[%d] Order: got %d want %d", i, got.Order, want.Order)
				}
			}
		})
	}
}
