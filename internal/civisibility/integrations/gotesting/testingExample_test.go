// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bufio"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExampleOutputMismatchMatchesTestingSemantics(t *testing.T) {
	tests := []struct {
		name      string
		got       string
		want      string
		unordered bool
		mismatch  bool
	}{
		{name: "ordered exact", got: "first\nsecond\n", want: "first\nsecond\n"},
		{name: "ordered trims surrounding whitespace", got: " first\n", want: "first"},
		{name: "ordered mismatch", got: "second\nfirst\n", want: "first\nsecond\n", mismatch: true},
		{name: "unordered", got: "second\nfirst\n", want: "first\nsecond\n", unordered: true},
		{name: "unordered preserves duplicates", got: "first\nfirst\n", want: "first\n", unordered: true, mismatch: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := exampleOutputMismatch(tt.got, tt.want, tt.unordered)
			require.Equal(t, tt.mismatch, message != "")
		})
	}
}

func TestExampleOutputLinesDoNotTruncateLongLines(t *testing.T) {
	longLine := strings.Repeat("x", bufio.MaxScanTokenSize+1)
	var lines []string
	forEachExampleOutputLine([]byte(longLine+"\r\nafter\n\r"), func(line string) {
		lines = append(lines, line)
	})
	require.Equal(t, []string{longLine, "after", ""}, lines)
}

func TestExampleOutputMismatchPreservesRawMessage(t *testing.T) {
	tests := []struct {
		name      string
		got       string
		want      string
		unordered bool
		message   string
	}{
		{name: "ordered whitespace", got: " actual \n\n", want: " expected \n", message: "got:\n actual \n\n\nwant:\n expected \n\n"},
		{name: "ordered empty output", want: " expected \n", message: "got:\n\nwant:\n expected \n\n"},
		{name: "ordered CRLF", got: "actual\r\n", want: "expected\r\n", message: "got:\nactual\r\n\nwant:\nexpected\r\n\n"},
		{name: "unordered whitespace", got: " actual \n\n", want: " expected \n", unordered: true, message: "got:\n actual \n\n\nwant (unordered):\n expected \n\n"},
		{name: "matching whitespace", got: " expected \n\n", want: "expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.message, exampleOutputMismatch(tt.got, tt.want, tt.unordered))
		})
	}
}

func TestCaptureExampleOutputPreservesPartialOutputAndError(t *testing.T) {
	wantErr := errors.New("read failed")
	result := captureExampleOutput(&failingExampleOutputReader{err: wantErr})

	require.Equal(t, "partial output", result.output)
	require.ErrorIs(t, result.err, wantErr)
}

func TestRunManagedExampleCapturesCompletion(t *testing.T) {
	result := runManagedExample(func() {})

	require.True(t, result.finished)
	require.Nil(t, result.panicData)
	require.Empty(t, result.stack)
}

func TestRunManagedExampleCapturesPanic(t *testing.T) {
	result := runManagedExample(func() { panic("example panic") })

	require.False(t, result.finished)
	require.Equal(t, "example panic", result.panicData)
	require.NotEmpty(t, result.stack)
}

func TestRunManagedExampleCapturesGoexit(t *testing.T) {
	result := runManagedExample(runtime.Goexit)

	require.False(t, result.finished)
	require.Nil(t, result.panicData)
	require.NotEmpty(t, result.stack)
}

type failingExampleOutputReader struct {
	err  error
	read bool
}

func (r *failingExampleOutputReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, r.err
	}
	r.read = true
	return copy(buffer, "partial output"), r.err
}
