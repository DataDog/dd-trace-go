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

// TestCaseAliasedImport checks that a logger is correlated when log/slog is
// imported under another name.
type TestCaseAliasedImport struct{}

func (*TestCaseAliasedImport) Setup(context.Context, *testing.T) {}

func (*TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, spanCtx := tracer.StartSpanFromContext(ctx, "slog.test")
	defer span.Finish()

	var logs bytes.Buffer
	stdslog.New(stdslog.NewJSONHandler(&logs, nil)).InfoContext(spanCtx, "alias")

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 1)
	requireCorrelated(t, records["alias"], span)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int { return 1 }

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}
