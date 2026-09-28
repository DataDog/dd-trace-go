// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bufio"
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
