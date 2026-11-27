// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package logrus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// correlation logs through logger with a span in the context, without a
// //dd:span function in between, and checks the exact fields of every entry
// and that logger holds exactly one correlation hook.
type correlation struct {
	logger *logrus.Logger
	logs   *bytes.Buffer
}

// newLogs returns the formatter and output a test logger writes with.
func (c *correlation) newLogs() (*bytes.Buffer, logrus.Formatter) {
	c.logs = new(bytes.Buffer)
	return c.logs, &logrus.JSONFormatter{}
}

func (c *correlation) Run(ctx context.Context, t *testing.T) {
	requireOneContextLogHook(t, c.logger)

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	for _, level := range []logrus.Level{logrus.DebugLevel, logrus.InfoLevel, logrus.WarnLevel, logrus.ErrorLevel} {
		c.logger.WithContext(ctx).Log(level, level.String())
	}
	span.Finish()

	requireCorrelatedEntries(t, c.logs, span, "debug", "info", "warning", "error")
}

func (*correlation) ExpectedSpanCount() int {
	return 1
}

func (*correlation) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
		},
	}
}

// requireOneContextLogHook checks that every level of logger fires exactly one
// correlation hook. The type is matched by name because orchestrion injects
// its own copy of DDContextLogHook into logrus.
func requireOneContextLogHook(t *testing.T, logger *logrus.Logger) {
	t.Helper()
	for _, level := range logrus.AllLevels {
		var n int
		for _, h := range logger.Hooks[level] {
			if strings.HasSuffix(fmt.Sprintf("%T", h), ".DDContextLogHook") {
				n++
			}
		}
		require.Equal(t, 1, n, "DDContextLogHook count at level %s", level)
	}
}

// requireCorrelatedEntries checks that logs holds one JSON entry per message,
// in order, each with exactly the IDs of span and no other added field.
func requireCorrelatedEntries(t *testing.T, logs *bytes.Buffer, span *tracer.Span, msgs ...string) {
	t.Helper()
	t.Logf("got logs:\n%s", logs)
	want := map[string]any{
		"dd.trace_id": span.Context().TraceID(),
		"dd.span_id":  strconv.FormatUint(span.Context().SpanID(), 10),
	}

	var got []string
	s := bufio.NewScanner(logs)
	for s.Scan() {
		var entry map[string]any
		require.NoError(t, json.Unmarshal(s.Bytes(), &entry))
		got = append(got, fmt.Sprint(entry["msg"]))
		assert.Equal(t, entry["msg"], entry["level"])
		delete(entry, "time")
		delete(entry, "msg")
		delete(entry, "level")
		assert.Equal(t, want, entry, "fields of entry %q", got[len(got)-1])
	}
	require.NoError(t, s.Err())
	require.Equal(t, msgs, got)
}
