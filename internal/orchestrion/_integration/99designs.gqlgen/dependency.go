// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gqlgen

import (
	"context"
	"testing"

	"example.com/gqlgendep"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/99designs.gqlgen/generated/graph"
)

// TestCaseDependency checks that a server created in a dependency module, not
// in the module being built, is traced.
type TestCaseDependency struct {
	TestCase
}

func (tc *TestCaseDependency) Setup(context.Context, *testing.T) {
	tc.server = gqlgendep.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	tc.server.AddTransport(transport.POST{})
}
