package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// spinner draws an in-place animated progress line on a TTY: a braille frame
// cycles while a label describes the current step. It rewrites a single line
// (carriage return + clear-to-end-of-line) so it never floods the scrollback,
// and clears the line on stop. All output goes to the given writer (stderr).
type spinner struct {
	w     io.Writer
	stop  chan struct{}
	done  chan struct{}
	mu    sync.Mutex
	label string
}

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// startSpinner launches the animation in a goroutine and returns immediately.
func startSpinner(w io.Writer, label string) *spinner {
	s := &spinner{
		w:     w,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		label: label,
	}
	go s.run()
	return s
}

func (s *spinner) run() {
	t := time.NewTicker(90 * time.Millisecond)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			fmt.Fprint(s.w, "\r\033[K") // clear the line on exit
			close(s.done)
			return
		case <-t.C:
			s.mu.Lock()
			label := s.label
			s.mu.Unlock()
			fmt.Fprintf(s.w, "\r\033[K%c %s", spinnerFrames[i%len(spinnerFrames)], label)
			i++
		}
	}
}

// setLabel updates the text shown next to the spinner.
func (s *spinner) setLabel(label string) {
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

// stopAndClear stops the animation and clears the line; safe to call once.
func (s *spinner) stopAndClear() {
	close(s.stop)
	<-s.done
}
