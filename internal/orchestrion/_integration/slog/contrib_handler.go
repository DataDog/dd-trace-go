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

	"github.com/stretchr/testify/require"

	slogtrace "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseContribHandler checks that a handler the application wrapped with
// the contrib itself is not wrapped again, so its records hold the IDs once.
// Orchestrion wraps it again and its records hold the IDs twice, so it only
// runs under otelc.
type TestCaseContribHandler struct{}

func (*TestCaseContribHandler) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion wraps a handler of the contrib again, so its records hold the IDs twice")
	}
}

func (*TestCaseContribHandler) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "slog.test")
	defer span.Finish()

	var logs bytes.Buffer
	wrapped := slog.New(slogtrace.WrapHandler(slog.NewJSONHandler(&logs, nil)))
	wrapped.InfoContext(ctx, "wrap-handler")
	slog.New(slogtrace.NewJSONHandler(&logs, nil)).InfoContext(ctx, "new-json-handler")
	slog.New(wrapped.Handler()).InfoContext(ctx, "same-handler")
	slog.New(wrapped.WithGroup("group").Handler()).InfoContext(ctx, "same-handler-group", "key", "value")

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 4)
	for _, msg := range []string{"wrap-handler", "new-json-handler", "same-handler", "same-handler-group"} {
		requireCorrelated(t, records[msg], span)
	}
	requireNotCorrelated(t, records["same-handler-group"].group(t, "group"))
}

func (*TestCaseContribHandler) ExpectedSpanCount() int { return 1 }

func (*TestCaseContribHandler) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}
