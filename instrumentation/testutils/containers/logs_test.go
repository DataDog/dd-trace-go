// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// recordingTB records Log calls and collects Cleanup functions instead of
// running them with the test, so the test can run the consumer's cleanup at
// a chosen moment and then keep delivering lines the way testcontainers'
// log-production goroutine does after a test completes.
type recordingTB struct {
	testing.TB
	mu       sync.Mutex
	logged   []string
	cleanups []func()
}

func (r *recordingTB) Log(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logged = append(r.logged, fmt.Sprint(args...))
}

func (r *recordingTB) Cleanup(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, f)
}

func (r *recordingTB) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.logged...)
}

// TestLogConsumerStopsLoggingAfterCleanup pins the fix for the mongo CI
// flake: testcontainers' log-production goroutine can call Accept after the
// test has completed, and a t.Log call at that point panics the test binary.
// Once the consumer's cleanup has run, no line may reach t.Log; lines
// delivered before that are still logged, in order.
func TestLogConsumerStopsLoggingAfterCleanup(t *testing.T) {
	rec := &recordingTB{}
	consumer := TestLogConsumer(rec)

	consumer.Accept(testcontainers.Log{Content: []byte("during the test\n")})
	waitFor(t, func() bool { return len(rec.lines()) == 1 },
		"the first line was not logged")

	// The second line races with cleanup: whichever wins, it must be
	// logged, not dropped, because cleanup is still allowed to log.
	consumer.Accept(testcontainers.Log{Content: []byte("late but during cleanup\n")})
	for _, f := range rec.cleanups {
		f()
	}
	consumer.Accept(testcontainers.Log{Content: []byte("after completion\n")})

	waitFor(t, func() bool { return len(rec.lines()) >= 2 },
		"the line delivered during cleanup was not logged")
	lines := rec.lines()
	if len(lines) != 2 {
		t.Fatalf("log delivered past cleanup: %q", lines)
	}
	if lines[0] != "during the test\n" || lines[1] != "late but during cleanup\n" {
		t.Fatalf("unexpected log lines: %q", lines)
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}
