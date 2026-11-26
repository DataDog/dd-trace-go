// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zap

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runTestExact is runTest, and also checks that logFn wrote wantLines lines
// and that each line has the trace fields once, so a call wrapped twice fails.
func runTestExact(ctx context.Context, t *testing.T, buf *bytes.Buffer, wantLines int, logFn func(context.Context)) {
	t.Helper()
	runTest(ctx, t, buf, logFn)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, wantLines, "wrong number of log lines")
	for _, line := range lines {
		assert.Equal(t, 1, strings.Count(line, `"dd.trace_id"`), "dd.trace_id not exactly once in: %s", line)
		assert.Equal(t, 1, strings.Count(line, `"dd.span_id"`), "dd.span_id not exactly once in: %s", line)
	}
}
