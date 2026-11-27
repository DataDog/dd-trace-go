// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"testing"

	"example.com/aerospikedep"
	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a call made in a dependency module, not in
// the module being built, is traced exactly once and gets the caller's
// context.
type TestCaseDependency struct {
	client *as.Client
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "dependency-key")
	require.NoError(t, err)
	require.NoError(t, aerospikedep.Put(ctx, tc.client, key, "dep"))
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put")},
		},
	}
}
