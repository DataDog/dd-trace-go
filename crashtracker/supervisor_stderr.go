// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	stderrCopyBufferSize   = 32 * 1024
	maxStderrLineSize      = 4 * 1024
	stderrPanicPrefix      = "panic: "
	stderrFatalPrefix      = "fatal error: "
	stderrStackLimitPrefix = "runtime: goroutine stack exceeds "
)

// stderrCrashLines records crash evidence and the last fatal and stack limit lines.
type stderrCrashLines struct {
	panicked   bool
	fatal      string
	stackLimit string
}

func (l *stderrCrashLines) remember(line []byte) {
	switch {
	case bytes.HasPrefix(line, []byte(stderrPanicPrefix)):
		l.panicked = true
	case bytes.HasPrefix(line, []byte(stderrFatalPrefix)):
		l.fatal = string(line)
	case bytes.HasPrefix(line, []byte(stderrStackLimitPrefix)):
		l.stackLimit = string(bytes.TrimRight(line, "\r"))
	}
}

// stderrLineScanner caps the stored part of a line, not the bytes it scans.
// Only a newline can start a new line, even after the buffer fills.
type stderrLineScanner struct {
	line  [maxStderrLineSize]byte
	size  int
	lines stderrCrashLines
}

func (s *stderrLineScanner) scan(p []byte) {
	for len(p) != 0 {
		line, rest, newline := bytes.Cut(p, []byte{'\n'})
		s.size += copy(s.line[s.size:], line)
		if !newline {
			return
		}
		s.lines.remember(s.line[:s.size])
		s.size = 0
		p = rest
	}
}

func (s *stderrLineScanner) snapshot() stderrCrashLines {
	lines := s.lines
	lines.remember(s.line[:s.size])
	return lines
}

// stderrTee copies the application's stderr to the original stderr and saves
// only the crash lines. A nil *stderrTee is valid and does nothing.
type stderrTee struct {
	read  *os.File // the supervisor end of the pipe
	write *os.File // the application end of the pipe
	sink  *os.File // a duplicate of the original stderr
	done  chan struct{}

	mu      sync.Mutex
	scanner stderrLineScanner
}

// startStderrTee makes the application write its stderr to a pipe that the
// supervisor copies to the original stderr. It returns nil, and leaves the
// stderr of the application alone, when a tee does not apply or cannot start.
func startStderrTee(cmd *exec.Cmd, terminalMode bool) *stderrTee {
	sink := openStderrSink(terminalMode)
	if sink == nil {
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		log.Warn("crashtracker: create stderr pipe: %v", err.Error()) //errtrack:ignore pipe creation failure
		_ = sink.Close()
		return nil
	}
	t := &stderrTee{read: r, write: w, sink: sink, done: make(chan struct{})}
	cmd.Stderr = w
	go t.copy()
	return t
}

// copy forwards the application's stderr until the pipe reaches EOF. A failed
// write to the original stderr drops that chunk. The loop keeps reading, so
// the application never blocks on a broken stderr.
func (t *stderrTee) copy() {
	defer close(t.done)
	buf := make([]byte, stderrCopyBufferSize)
	for {
		n, err := t.read.Read(buf)
		if n > 0 {
			t.mu.Lock()
			t.scanner.scan(buf[:n])
			t.mu.Unlock()
			_, _ = t.sink.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// childStarted closes the supervisor's copy of the application end of the pipe.
// The reader then reaches EOF when the application and its descendants close
// theirs.
func (t *stderrTee) childStarted() {
	if t != nil {
		_ = t.write.Close()
	}
}

// abort undoes the tee after a failed start of the application.
func (t *stderrTee) abort() {
	if t == nil {
		return
	}
	_ = t.write.Close()
	_ = t.read.Close()
	<-t.done
	_ = t.sink.Close()
}

// finish waits up to timeout for the copy to reach EOF, so that no byte is
// lost after a normal exit. A descendant of the application can hold the pipe
// open. Then finish returns the crash lines that the tee saw before timeout.
func (t *stderrTee) finish(timeout time.Duration) stderrCrashLines {
	if t == nil {
		return stderrCrashLines{}
	}
	select {
	case <-t.done:
	case <-time.After(timeout):
		_ = t.read.Close()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scanner.snapshot()
}
