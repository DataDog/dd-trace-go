// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// passthrough forwards every call to next, like a third-party handler built
// around logger.Handler().
type passthrough struct {
	next slog.Handler
}

func (h passthrough) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h passthrough) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

func (h passthrough) WithAttrs(attrs []slog.Attr) slog.Handler {
	return passthrough{next: h.next.WithAttrs(attrs)}
}

func (h passthrough) WithGroup(name string) slog.Handler {
	return passthrough{next: h.next.WithGroup(name)}
}

func setOtelcEnabled(t *testing.T, enabled bool) {
	orig := otelcEnabled
	otelcEnabled = func() bool { return enabled }
	t.Cleanup(func() { otelcEnabled = orig })
}

func startSpan(t *testing.T) context.Context {
	tracer.Start(
		tracer.WithTraceEnabled(true),
		tracer.WithLogger(testutils.DiscardLogger()),
	)
	t.Cleanup(tracer.Stop)
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	t.Cleanup(func() { span.Finish() })
	return ctx
}

func TestNestedHandler(t *testing.T) {
	for _, tc := range []struct {
		name   string
		otelc  bool
		traces int
	}{
		{name: "otelc", otelc: true, traces: 1},
		{name: "plain", otelc: false, traces: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setOtelcEnabled(t, tc.otelc)
			ctx := startSpan(t)

			var b bytes.Buffer
			inner := WrapHandler(slog.NewJSONHandler(&b, nil))
			slog.New(WrapHandler(passthrough{next: inner})).InfoContext(ctx, "nested")

			assert.Equal(t, tc.traces, strings.Count(b.String(), `"`+ext.LogKeyTraceID+`"`), b.String())
			assert.Equal(t, tc.traces, strings.Count(b.String(), `"`+ext.LogKeySpanID+`"`), b.String())
		})
	}
}

func TestNestedHandlerInnerGroup(t *testing.T) {
	setOtelcEnabled(t, true)
	ctx := startSpan(t)

	var b bytes.Buffer
	inner := WrapHandler(slog.NewJSONHandler(&b, nil)).WithGroup("g")
	slog.New(WrapHandler(passthrough{next: inner})).InfoContext(ctx, "nested", "key", "value")

	// The outer handler's IDs go into the group the inner one has open, so the
	// inner one still adds them at the root level.
	var entry map[string]any
	require.NoError(t, json.Unmarshal(b.Bytes(), &entry))
	assert.Contains(t, entry, ext.LogKeyTraceID)
	assert.Contains(t, entry, ext.LogKeySpanID)
	require.IsType(t, map[string]any{}, entry["g"])
	assert.Contains(t, entry["g"], ext.LogKeyTraceID)
}

func TestNestedHandlerOuterGroup(t *testing.T) {
	setOtelcEnabled(t, true)
	ctx := startSpan(t)

	for name, wrap := range map[string]func(slog.Handler) slog.Handler{
		"direct":      func(h slog.Handler) slog.Handler { return h },
		"passthrough": func(h slog.Handler) slog.Handler { return passthrough{next: h} },
	} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			inner := WrapHandler(slog.NewJSONHandler(&b, nil))
			slog.New(WrapHandler(wrap(inner))).WithGroup("g").InfoContext(ctx, "nested", "key", "value")

			// The outer handler adds its IDs at the root level before it opens
			// its groups on the inner one, which must not add them again.
			assert.Equal(t, 1, strings.Count(b.String(), `"`+ext.LogKeyTraceID+`"`), b.String())
			assert.Equal(t, 1, strings.Count(b.String(), `"`+ext.LogKeySpanID+`"`), b.String())
			var entry map[string]any
			require.NoError(t, json.Unmarshal(b.Bytes(), &entry))
			assert.Contains(t, entry, ext.LogKeyTraceID)
			assert.Equal(t, map[string]any{"key": "value"}, entry["g"])
		})
	}
}
