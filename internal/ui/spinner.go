package ui

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Spinner is a minimal in-place spinner for pretty mode. It is intentionally
// small: we are not pulling in Bubble Tea for a one-line indicator while a
// stream is between events. It writes to a TTY, clears its own line, and is
// safe to Stop multiple times.
//
// Usage pattern in a streaming command:
//
//	sp := ui.NewSpinner(out, "working")
//	sp.Start()
//	// on each event: sp.Pause(); render(event); sp.Resume()
//	// at the end:    sp.Stop()
type Spinner struct {
	out    io.Writer
	label  string
	frames []string

	mu      sync.Mutex
	active  bool
	stop    chan struct{}
	stopped bool
}

// NewSpinner builds a spinner that writes to out with the given label.
func NewSpinner(out io.Writer, label string) *Spinner {
	return &Spinner{
		out:    out,
		label:  label,
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		stop:   make(chan struct{}),
	}
}

// Start begins animating in a background goroutine.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.active || s.stopped {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				if s.active {
					fmt.Fprintf(s.out, "\r%s %s", s.frames[i%len(s.frames)], s.label)
				}
				s.mu.Unlock()
				i++
			}
		}
	}()
}

// clearLine erases the current spinner line so the next write starts clean.
func (s *Spinner) clearLine() {
	fmt.Fprint(s.out, "\r\033[K")
}

// Pause hides the spinner so a real line of output can be written without the
// spinner frame bleeding into it.
func (s *Spinner) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.active = false
		s.clearLine()
	}
}

// Resume re-enables drawing after a Pause.
func (s *Spinner) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.active = true
	}
}

// Stop permanently halts the spinner and clears its line.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.active = false
	s.clearLine()
	s.mu.Unlock()
	close(s.stop)
}
