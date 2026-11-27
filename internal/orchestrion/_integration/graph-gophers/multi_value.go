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

// TestCaseMultiValueArgs checks that a schema is traced when the caller passes
// every argument as one multi-value call.
type TestCaseMultiValueArgs struct {
	TestCase
}

func schemaArgs() (string, any) {
	return schema, new(resolver)
}

func (tc *TestCaseMultiValueArgs) Setup(_ context.Context, t *testing.T) {
	if built.WithOrchestrion {
		t.Skip("orchestrion appends the tracer after the multi-value call, which does not build")
	}
	//orchestrion:ignore
	s, err := graphql.ParseSchema(schemaArgs())
	require.NoError(t, err)
	tc.serve(t, s)
}

func (*TestCaseMultiValueArgs) ExpectedSpanCount() int {
	return spansPerRequest
}
