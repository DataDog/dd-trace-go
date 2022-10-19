// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

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

// Tick the stopwatch, saving the current time and returning the duration
// between now and the last tick.  If no tick occurred, start time of the
// stopwatch is used.
func (s *Stopwatch) Tick() time.Duration {
	now := time.Now()
	td := now.Sub(s.prev)
	s.prev = now
	return td
}
