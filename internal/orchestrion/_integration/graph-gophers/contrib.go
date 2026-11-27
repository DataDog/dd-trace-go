// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"testing"

	"github.com/graph-gophers/graphql-go"

	graphqltrace "github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2"
)

// TestCaseContribTracer checks that a schema the application already traces
// with the contrib is traced once.
type TestCaseContribTracer struct {
	TestCase
}

func (tc *TestCaseContribTracer) Setup(_ context.Context, t *testing.T) {
	tc.serve(t, graphql.MustParseSchema(schema, new(resolver), graphql.Tracer(graphqltrace.NewTracer())))
}

func (*TestCaseContribTracer) ExpectedSpanCount() int {
	return spansPerRequest
}
