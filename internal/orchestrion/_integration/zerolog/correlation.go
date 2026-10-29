// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package zerolog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// correlation logs through logger with and without a span in the context and
// checks the exact fields of every event and how many correlation hooks the
// logger holds.
type correlation struct {
	logger zerolog.Logger
	logs   *bytes.Buffer
	// hooks is the number of DDContextLogHook values the logger holds. Zero
	// means one.
	hooks int
}

func (c *correlation) newLogs() *bytes.Buffer {
	c.logs = new(bytes.Buffer)
	return c.logs
}

func (c *correlation) Run(ctx context.Context, t *testing.T) {
	wantHooks := c.hooks
	if wantHooks == 0 {
		wantHooks = 1
	}
	require.Equal(t, wantHooks, contextLogHooks(c.logger), "DDContextLogHook count")

	span, spanCtx := tracer.StartSpanFromContext(ctx, "test.root")
	logger := c.logger.Level(zerolog.DebugLevel)
	logger.Debug().Ctx(spanCtx).Msg("debug")
	logger.Info().Ctx(spanCtx).Msg("info")
	logger.Warn().Ctx(spanCtx).Msg("warn")
	logger.Error().Ctx(spanCtx).Msg("error")
	span.Finish()
	// After Finish, so the goroutine has no active span either.
	logger.Info().Ctx(ctx).Msg("no span")

	requireEvents(t, c.logs, span, "debug", "info", "warn", "error", "no span")
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

// contextLogHooks returns how many DDContextLogHook values l holds. zerolog
// does not export its hooks, so this reads them through reflection. The type
// is matched by name because orchestrion injects its own copy of
// DDContextLogHook into zerolog.
func contextLogHooks(l zerolog.Logger) int {
	hooks := reflect.ValueOf(l).FieldByName("hooks")
	var n int
	for i := range hooks.Len() {
		h := hooks.Index(i).Elem().Type()
		if h.Kind() == reflect.Pointer {
			h = h.Elem()
		}
		if h.Name() == "DDContextLogHook" {
			n++
		}
	}
	return n
}

// requireEvents checks that logs holds one JSON event per message, in order.
// Each event logged as "no span" has no trace fields, and every other event has
// exactly one dd.trace_id and one dd.span_id, those of span.
func requireEvents(t *testing.T, logs *bytes.Buffer, span *tracer.Span, msgs ...string) {
	t.Helper()
	t.Logf("got logs:\n%s", logs)
	correlated := map[string]any{
		"dd.trace_id": span.Context().TraceID(),
		"dd.span_id":  strconv.FormatUint(span.Context().SpanID(), 10),
	}

	var got []string
	s := bufio.NewScanner(logs)
	for s.Scan() {
		line := s.Text()
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		msg, _ := event["message"].(string)
		got = append(got, msg)
		delete(event, "message")
		delete(event, "level")
		delete(event, "time") // the global logger adds it

		want, n := correlated, 1
		if msg == "no span" {
			want, n = map[string]any{}, 0
		}
		assert.Equal(t, want, event, "fields of event %q", msg)
		// Unmarshal keeps the last of duplicate keys, so count them in the line.
		for key := range correlated {
			assert.Equal(t, n, strings.Count(line, strconv.Quote(key)), "%s in event %q", key, msg)
		}
	}
	require.NoError(t, s.Err())
	require.Equal(t, msgs, got)
}
