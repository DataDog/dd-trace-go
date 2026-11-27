// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"example.com/slogdep"
	"github.com/stretchr/testify/require"

	slogtrace "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseNestedHandler checks a handler that holds one of the contrib inside
// another handler, which the rules wrap again. The records must hold the IDs
// once at the root level. Orchestrion has no way to tell and its records hold
// the IDs twice, so it only runs under otelc.
type TestCaseNestedHandler struct{}

func (*TestCaseNestedHandler) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion wraps a handler that holds one of the contrib again, so its records hold the IDs twice")
	}
}

func (*TestCaseNestedHandler) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "slog.test")
	defer span.Finish()

	var logs bytes.Buffer
	slog.New(&passthrough{next: slogtrace.WrapHandler(slog.NewJSONHandler(&logs, nil))}).InfoContext(ctx, "contrib-inside")
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	slogdep.Passthrough(logger).InfoContext(ctx, "dependency-passthrough")
	slogdep.Passthrough(logger.WithGroup("group")).InfoContext(ctx, "dependency-passthrough-group", "key", "value")
	slogdep.Passthrough(logger).WithGroup("group").InfoContext(ctx, "dependency-passthrough-then-group", "key", "value")

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 4)
	for _, msg := range []string{"contrib-inside", "dependency-passthrough", "dependency-passthrough-group", "dependency-passthrough-then-group"} {
		requireCorrelated(t, records[msg], span)
	}
	requireNotCorrelated(t, records["dependency-passthrough-then-group"].group(t, "group"))
	// The outer handler passes its IDs to the inner one with WithAttrs, which
	// puts them in the group the inner one already has open. The inner one
	// still adds its own at the root level.
	requireCorrelated(t, records["dependency-passthrough-group"].group(t, "group"), span)
}

func (*TestCaseNestedHandler) ExpectedSpanCount() int { return 1 }

func (*TestCaseNestedHandler) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}

// passthrough forwards every call to next.
type passthrough struct {
	next slog.Handler
}

func (h *passthrough) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *passthrough) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

func (h *passthrough) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &passthrough{next: h.next.WithAttrs(attrs)}
}

func (h *passthrough) WithGroup(name string) slog.Handler {
	return &passthrough{next: h.next.WithGroup(name)}
}
