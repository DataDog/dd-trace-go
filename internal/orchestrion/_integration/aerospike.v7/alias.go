// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"testing"

	aero "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a call is traced exactly once when
// aerospike is imported under another name.
type TestCaseAliasedImport struct {
	client *aero.Client
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := aero.NewKey("test", "testset", "alias-key")
	require.NoError(t, err)
	require.NoError(t, tc.client.Put(nil, key, aero.BinMap{"value": "alias"}))
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put")},
		},
	}
}
