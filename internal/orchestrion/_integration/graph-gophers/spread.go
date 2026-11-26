// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/graph-gophers/graphql-go"
	"github.com/stretchr/testify/require"
)

// TestCaseSpreadOptions checks that a schema is traced when the caller spreads
// a slice of options, and that the tracer is not written into the spare
// capacity of that slice.
type TestCaseSpreadOptions struct {
	TestCase
}

func (tc *TestCaseSpreadOptions) Setup(_ context.Context, t *testing.T) {
	if built.WithOrchestrion {
		t.Skip("orchestrion appends the tracer as an any, which does not build when the caller spreads a slice")
	}
	backing := make([]graphql.SchemaOpt, 2)
	opts := backing[:1]
	opts[0] = graphql.UseFieldResolvers()

	//orchestrion:ignore
	s, err := graphql.ParseSchema(schema, new(resolver), opts...)
	require.NoError(t, err)
	require.Nil(t, backing[1], "the caller's options slice was modified")
	tc.serve(t, s)
}

func (*TestCaseSpreadOptions) ExpectedSpanCount() int {
	return spansPerRequest
}
