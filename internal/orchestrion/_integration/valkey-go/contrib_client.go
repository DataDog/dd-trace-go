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

	valkeytrace "github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseContribClient checks that a client built with the contrib is traced
// once, with the contrib's options.
type TestCaseContribClient struct {
	valkeyServer
	client valkey.Client
}

func (tc *TestCaseContribClient) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.client, err = valkeytrace.NewClient(valkey.ClientOption{InitAddress: []string{tc.addr}}, valkeytrace.WithService("contrib.client"))
	require.NoError(t, err)
	t.Cleanup(tc.client.Close)
}

func (tc *TestCaseContribClient) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, tc.client.Do(ctx, tc.client.B().Set().Key("contrib").Value("value").Build()).Error())
}

func (*TestCaseContribClient) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseContribClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{commandSpan("contrib.client", "SET")},
		},
	}
}
