// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	"testing"

	"example.com/slogdep"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a logger built in a dependency module, not in
// the module being built, holds the IDs once.
type TestCaseDependency struct{}

func (*TestCaseDependency) Setup(context.Context, *testing.T) {}

func (*TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "slog.test")
	defer span.Finish()

	var logs bytes.Buffer
	slogdep.NewLogger(&logs).InfoContext(ctx, "dependency")

	records := parseRecords(t, logs.Bytes())
	require.Len(t, records, 1)
	requireCorrelated(t, records["dependency"], span)
}

func (*TestCaseDependency) ExpectedSpanCount() int { return 1 }

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "slog.test"}}}
}
