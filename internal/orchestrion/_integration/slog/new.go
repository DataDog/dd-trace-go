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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

var pkgLogs bytes.Buffer

// pkgLogger is built while the package is initialized, so the contrib must be
// initialized before it.
var pkgLogger = slog.New(slog.NewJSONHandler(&pkgLogs, nil))

// TestCaseNew checks that every record of a logger built with slog.New holds
// the trace and span IDs exactly once, at the root level.
type TestCaseNew struct{}

func (*TestCaseNew) Setup(context.Context, *testing.T) {}

func (*TestCaseNew) Run(ctx context.Context, t *testing.T) {
	span, spanCtx := tracer.StartSpanFromContext(ctx, "slog.test")

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.InfoContext(spanCtx, "new")
	logger.With("key", "value").InfoContext(spanCtx, "with")
	logger.WithGroup("group").InfoContext(spanCtx, "with-group", "key", "value")
	logger.Log(spanCtx, slog.LevelWarn, "log")
	logger.LogAttrs(spanCtx, slog.LevelError, "log-attrs")
	pkgLogger.InfoContext(spanCtx, "package-var")
	// Instrumented builds also find the active span of the goroutine, so the
	// span must be finished first.
	span.Finish()
	logger.InfoContext(ctx, "no-span")

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 6)
	for _, msg := range []string{"new", "with", "with-group", "log", "log-attrs"} {
		requireCorrelated(t, records[msg], span)
	}
	requireNotCorrelated(t, records["with-group"].group(t, "group"))
	requireNotCorrelated(t, records["no-span"])

	pkgRecords := parseRecords(t, pkgLogs.Bytes())
	require.Len(t, pkgRecords, 1)
	requireCorrelated(t, pkgRecords["package-var"], span)
}

func (*TestCaseNew) ExpectedSpanCount() int { return 1 }

func (*TestCaseNew) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}
