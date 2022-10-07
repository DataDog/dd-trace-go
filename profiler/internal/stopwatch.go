package internal

import "time"

// Stopwatch is used to time code execution.
type Stopwatch struct {
	start time.Time
	prev  time.Time
}

// NewStopwatch creates a new stopwatch
func NewStopwatch() *Stopwatch {
	now := time.Now()
	return &Stopwatch{start: now, prev: now}
}

// Reset zeros a stopwatch back to the current time
func (s *Stopwatch) Reset() {
	now := time.Now()
	s.start = now
	s.prev = now
}

// Duration returns the total duration since this stopwatch began.
func (s *Stopwatch) Duration() time.Duration {
	now := time.Now()
	return now.Sub(s.start)
}

// LastDuration returns the total time since the last ticked time.
func (s *Stopwatch) LastDuration() time.Duration {
	now := time.Now()
	return now.Sub(s.prev)
}

// Tick the stopwatch, saving the current time and returning the duration
// between now and the last tick.  If no tick occured, start time of the
// stopwatch is used.
func (s *Stopwatch) Tick() time.Duration {
	now := time.Now()
	td := now.Sub(s.prev)
	s.prev = now
	return td
}

// Last returns the last ticked time.
func (s *Stopwatch) Last() time.Time {
	return s.prev
}
