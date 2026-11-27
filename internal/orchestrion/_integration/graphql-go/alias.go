// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import (
	"context"
	"testing"

	gql "github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a schema is traced when graphql-go is
// imported under another name.
type TestCaseAliasedImport struct {
	schema gql.Schema
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.schema, err = gql.NewSchema(helloConfig())
	require.NoError(t, err)
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	queryHello(ctx, t, tc.schema)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return spansPerQuery
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return expectHelloTrace()
}
