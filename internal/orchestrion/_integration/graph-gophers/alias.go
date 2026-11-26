// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"testing"

	gql "github.com/graph-gophers/graphql-go"
	"github.com/stretchr/testify/require"
)

// TestCaseAliasedImport checks that a schema is traced when graphql-go is
// imported under another name.
type TestCaseAliasedImport struct {
	TestCase
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	s, err := gql.ParseSchema(schema, new(resolver))
	require.NoError(t, err)
	tc.serve(t, s)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return spansPerRequest
}
