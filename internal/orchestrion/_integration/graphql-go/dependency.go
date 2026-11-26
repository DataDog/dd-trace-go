// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import (
	"context"
	"testing"

	"example.com/graphqlgodep"
	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a schema built in a dependency module, not in
// the module being built, is traced.
type TestCaseDependency struct {
	schema graphql.Schema
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.schema, err = graphqlgodep.NewSchema(helloConfig())
	require.NoError(t, err)
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	queryHello(ctx, t, tc.schema)
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return spansPerQuery
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return expectHelloTrace()
}
