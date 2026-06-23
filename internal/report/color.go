package report

// ANSI SGR codes used for colorized text output. Kept tiny and dependency-free.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

// palette wraps text in ANSI codes when enabled, and is a no-op otherwise, so
// the same formatting code path serves both colored and plain output.
type palette struct{ on bool }

func (p palette) wrap(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (p palette) name(s string) string   { return p.wrap(ansiBold+ansiCyan, s) } // variable name
func (p palette) loc(s string) string    { return p.wrap(ansiGreen, s) }         // file:line
func (p palette) winner(s string) string { return p.wrap(ansiBold+ansiGreen, s) }
func (p palette) dim(s string) string    { return p.wrap(ansiDim, s) }  // modes, confidence, values
func (p palette) warn(s string) string   { return p.wrap(ansiYellow, s) } // warnings, inherited
func (p palette) bad(s string) string    { return p.wrap(ansiDim, s) }    // "not set"
