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

// TestCaseOptionCall passes the client option as a call, which must run once.
type TestCaseOptionCall struct {
	valkeyServer
	optionCalls int
}

func (*TestCaseOptionCall) Setup(context.Context, *testing.T) {}

func (tc *TestCaseOptionCall) option() valkey.ClientOption {
	tc.optionCalls++
	return valkey.ClientOption{InitAddress: []string{tc.addr}, SelectDB: 1}
}

func (tc *TestCaseOptionCall) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	client, err := valkey.NewClient(tc.option())
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, 1, tc.optionCalls)
	require.NoError(t, client.Do(ctx, client.B().Get().Key("option-call").Build()).NonValkeyError())
}

func (*TestCaseOptionCall) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseOptionCall) ExpectedTraces() trace.Traces {
	span := commandSpan("valkey.client", "GET")
	span.Meta["out.db"] = "1"
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{span},
		},
	}
}
