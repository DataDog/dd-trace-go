// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"testing"

	graphqlgo "github.com/graph-gophers/graphql-go"
)

// TestCaseShadowedAlias checks that a schema is traced when the function that
// parses it has a parameter named like the package. The rules add their own
// import of graphql-go, and a name in scope must not hide it.
type TestCaseShadowedAlias struct {
	TestCase
}

func mustParseShadowed(graphql string) *graphqlgo.Schema {
	return graphqlgo.MustParseSchema(graphql, new(resolver))
}

func (tc *TestCaseShadowedAlias) Setup(_ context.Context, t *testing.T) {
	tc.serve(t, mustParseShadowed(schema))
}

func (*TestCaseShadowedAlias) ExpectedSpanCount() int {
	return spansPerRequest
}
