// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import (
	"context"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDo checks that a schema built with graphql.NewSchema traces each
// query once, with no HTTP layer around it.
type TestCaseDo struct {
	schema graphql.Schema
}

func (tc *TestCaseDo) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.schema, err = graphql.NewSchema(helloConfig())
	require.NoError(t, err)
}

func (tc *TestCaseDo) Run(ctx context.Context, t *testing.T) {
	queryHello(ctx, t, tc.schema)
}

func (*TestCaseDo) ExpectedSpanCount() int {
	return spansPerQuery
}

func (*TestCaseDo) ExpectedTraces() trace.Traces {
	return expectHelloTrace()
}
