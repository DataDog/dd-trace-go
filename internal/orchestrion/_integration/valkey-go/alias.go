// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package valkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	valkeyalias "github.com/valkey-io/valkey-go"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseAliasedImport struct {
	valkeyServer
	client valkeyalias.Client
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.client, err = valkeyalias.NewClient(valkeyalias.ClientOption{InitAddress: []string{tc.addr}})
	require.NoError(t, err)
	t.Cleanup(tc.client.Close)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, tc.client.Do(ctx, tc.client.B().Set().Key("alias").Value("value").Build()).Error())
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	// test.root and one valkey.command span.
	return 2
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{commandSpan("valkey.client", "SET")},
		},
	}
}
