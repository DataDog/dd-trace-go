// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStderrLineScannerKeepsLastCrashLines(t *testing.T) {
	var scanner stderrLineScanner
	for _, chunk := range []string{
		"fatal error: old\nruntime: goroutine stack exceeds 123-byte limit\nno",
		"t a fatal error: ignore\nfat",
		"al error: stack overflow\r\nruntime: goroutine sta",
		"ck exceeds 32768-byte limit\r\n",
		"after the crash lines",
	} {
		scanner.scan([]byte(chunk))
	}
	if got, want := stderrFatalMessage(scanner.snapshot()), "stack overflow (goroutine stack exceeds 32768-byte limit)"; got != want {
		t.Errorf("fatal message = %q, want %q", got, want)
	}
}

func TestStderrLineScannerCapsLongLines(t *testing.T) {
	var scanner stderrLineScanner
	want := "fatal error: " + strings.Repeat("x", maxStderrLineSize-len("fatal error: "))
	scanner.scan([]byte(want))
	scanner.scan([]byte("overflow\n" + strings.Repeat("x", maxStderrLineSize+1)))
	scanner.scan([]byte("fatal error: not at the start of a line\n"))
	if got := scanner.snapshot().fatal; got != want {
		t.Errorf("fatal line = %.60q (%d bytes), want %.60q (%d bytes)", got, len(got), want, len(want))
	}
	scanner.scan([]byte("fatal error: no newline"))
	if got := scanner.snapshot().fatal; got != "fatal error: no newline" {
		t.Errorf("fatal line = %q, want %q", got, "fatal error: no newline")
	}
}

func TestStderrTeeFinishTimeout(t *testing.T) {
	tee := newTestStderrTee(t)
	want := stderrCrashLines{
		fatal:      "fatal error: stack overflow",
		stackLimit: "runtime: goroutine stack exceeds 32768-byte limit",
	}
	_, err := io.WriteString(tee.write, want.stackLimit+"\n"+want.fatal+"\n")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		tee.mu.Lock()
		defer tee.mu.Unlock()
		return tee.scanner.snapshot() == want
	}, 5*time.Second, time.Millisecond)

	result := make(chan stderrCrashLines, 1)
	go func() { result <- tee.finish(0) }()
	select {
	case got := <-result:
		require.Equal(t, want, got)
	case <-time.After(5 * time.Second):
		t.Fatal("finish did not return with the write end open")
	}
	select {
	case <-tee.done:
	case <-time.After(5 * time.Second):
		t.Fatal("copy did not stop after finish timed out")
	}
	require.ErrorIs(t, tee.read.Close(), os.ErrClosed)
}

func TestStderrTeeAbort(t *testing.T) {
	tee := newTestStderrTee(t)
	done := make(chan struct{})
	go func() {
		tee.abort()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not return with the write end open")
	}
	select {
	case <-tee.done:
	default:
		t.Fatal("copy did not stop before abort returned")
	}
	for _, f := range []*os.File{tee.read, tee.write, tee.sink} {
		require.ErrorIs(t, f.Close(), os.ErrClosed, "%s is still open", f.Name())
	}
}

func newTestStderrTee(t *testing.T) *stderrTee {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = w.Close()
		_ = r.Close()
	})
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	tee := &stderrTee{read: r, write: w, sink: sink, done: make(chan struct{})}
	go tee.copy()
	return tee
}
