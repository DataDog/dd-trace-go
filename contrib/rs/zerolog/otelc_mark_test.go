// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package zerolog

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func setOtelcEnabled(t *testing.T, enabled bool) {
	t.Helper()
	prev := otelcEnabled
	otelcEnabled = func() bool { return enabled }
	t.Cleanup(func() { otelcEnabled = prev })
}

// logTwice logs two events through a logger that runs the hook twice, once
// with the context on the event and once with the context on the logger.
func logTwice(t *testing.T) (*tracer.Span, []string) {
	t.Helper()
	tracer.Start(tracer.WithLogStartup(false))
	t.Cleanup(tracer.Stop)
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test.root")
	defer span.Finish()

	var buf strings.Builder
	logger := zerolog.New(&buf).Hook(DDContextLogHook{}, &DDContextLogHook{})
	logger.Info().Ctx(ctx).Msg("event ctx")
	logger.Info().Ctx(ctx).Msg("event ctx again")
	withCtx := logger.With().Ctx(ctx).Logger()
	withCtx.Info().Msg("logger ctx")
	withCtx.Info().Msg("logger ctx again")

	var lines []string
	s := bufio.NewScanner(strings.NewReader(buf.String()))
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	require.NoError(t, s.Err())
	require.Len(t, lines, 4)
	return span, lines
}

func TestOtelcCorrelatesOnce(t *testing.T) {
	setOtelcEnabled(t, true)
	span, lines := logTwice(t)
	for _, line := range lines {
		assert.Equal(t, 1, strings.Count(line, `"dd.trace_id"`), line)
		assert.Equal(t, 1, strings.Count(line, `"dd.span_id"`), line)
		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &data))
		assert.Equal(t, span.Context().TraceID(), data["dd.trace_id"])
		assert.Equal(t, strconv.FormatUint(span.Context().SpanID(), 10), data["dd.span_id"])
	}
}

func TestPlainBuildCorrelatesPerHook(t *testing.T) {
	setOtelcEnabled(t, false)
	_, lines := logTwice(t)
	for _, line := range lines {
		assert.Equal(t, 2, strings.Count(line, `"dd.trace_id"`), line)
		assert.Equal(t, 2, strings.Count(line, `"dd.span_id"`), line)
	}
}

// forwardHook logs each event's context to another logger, as an application
// hook that copies events elsewhere might.
type forwardHook struct{ to zerolog.Logger }

func (h forwardHook) Run(e *zerolog.Event, _ zerolog.Level, msg string) {
	h.to.Info().Ctx(e.GetCtx()).Msg(msg)
}

func TestOtelcForwardedContextIsCorrelated(t *testing.T) {
	setOtelcEnabled(t, true)
	tracer.Start(tracer.WithLogStartup(false))
	t.Cleanup(tracer.Stop)
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test.root")
	defer span.Finish()

	var forwarded strings.Builder
	to := zerolog.New(&forwarded).Hook(DDContextLogHook{})
	logger := zerolog.New(io.Discard).Hook(DDContextLogHook{}, forwardHook{to: to})
	logger.Info().Ctx(ctx).Msg("forwarded")

	assert.Equal(t, 1, strings.Count(forwarded.String(), `"dd.trace_id"`), forwarded.String())
	assert.Equal(t, 1, strings.Count(forwarded.String(), `"dd.span_id"`), forwarded.String())
}

func TestOtelcNoSpan(t *testing.T) {
	setOtelcEnabled(t, true)
	var buf strings.Builder
	logger := zerolog.New(&buf).Hook(DDContextLogHook{})
	logger.Info().Ctx(context.Background()).Msg("no span")
	assert.NotContains(t, buf.String(), "dd.")
}
