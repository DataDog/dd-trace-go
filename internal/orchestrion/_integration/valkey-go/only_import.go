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
	"github.com/valkey-io/valkey-go"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseOnlyImport checks a file whose only use of the valkey-go import is
// the NewClient call. The rewritten file must still build.
type TestCaseOnlyImport struct {
	valkeyServer
	client valkeyClient
}

func (tc *TestCaseOnlyImport) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.client, err = valkey.NewClient(clientOption{InitAddress: []string{tc.addr}})
	require.NoError(t, err)
	t.Cleanup(tc.client.Close)
}

func (tc *TestCaseOnlyImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, tc.client.Do(ctx, tc.client.B().Set().Key("only-import").Value("value").Build()).Error())
}

func (*TestCaseOnlyImport) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseOnlyImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{commandSpan("valkey.client", "SET")},
		},
	}
}
