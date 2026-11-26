// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package valkey

import (
	"context"
	"testing"

	"example.com/valkeydep"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseDependency struct {
	valkeyServer
}

func (*TestCaseDependency) Setup(context.Context, *testing.T) {}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client, err := valkeydep.NewClient(tc.addr)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.Do(ctx, client.B().Get().Key("dependency").Build()).NonValkeyError())
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{commandSpan("valkey.client", "GET")},
		},
	}
}
