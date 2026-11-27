// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
)

// TestCaseMustParseSchema checks that a schema from graphql.MustParseSchema is
// traced once.
type TestCaseMustParseSchema struct {
	TestCase
}

func (tc *TestCaseMustParseSchema) Setup(_ context.Context, t *testing.T) {
	tc.serve(t, graphql.MustParseSchema(schema, new(resolver)))
}

func (*TestCaseMustParseSchema) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCase) serve(t *testing.T, s *graphql.Schema) {
	t.Helper()
	tc.server = httptest.NewServer(&relay.Handler{Schema: s})
	t.Cleanup(func() { tc.server.Close() })
}
