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

	graphqltrace "github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseContribSchema checks that a schema the application builds with the
// contrib itself is traced once, not twice.
type TestCaseContribSchema struct {
	schema graphql.Schema
}

func (tc *TestCaseContribSchema) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.schema, err = graphqltrace.NewSchema(helloConfig())
	require.NoError(t, err)
}

func (tc *TestCaseContribSchema) Run(ctx context.Context, t *testing.T) {
	queryHello(ctx, t, tc.schema)
}

func (*TestCaseContribSchema) ExpectedSpanCount() int {
	return spansPerQuery
}

func (*TestCaseContribSchema) ExpectedTraces() trace.Traces {
	return expectHelloTrace()
}
