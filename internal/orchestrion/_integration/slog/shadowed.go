// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	stdslog "log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseShadowedNames checks a slog.New call in a scope that declares slog
// and slogtrace, in a file that imports log/slog under another name. The
// instrumentation must not resolve to those locals.
type TestCaseShadowedNames struct{}

func (*TestCaseShadowedNames) Setup(context.Context, *testing.T) {}

func (*TestCaseShadowedNames) Run(ctx context.Context, t *testing.T) {
	span, spanCtx := tracer.StartSpanFromContext(ctx, "slog.test")

	var logs bytes.Buffer
	slog, slogtrace := "slog", "slogtrace"
	logger := stdslog.New(stdslog.NewJSONHandler(&logs, nil))
	logger.InfoContext(spanCtx, slog+slogtrace)
	span.Finish()

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 1)
	requireCorrelated(t, records["slogslogtrace"], span)
}

func (*TestCaseShadowedNames) ExpectedSpanCount() int { return 1 }

func (*TestCaseShadowedNames) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}
