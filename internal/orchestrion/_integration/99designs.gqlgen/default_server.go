// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gqlgen

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/99designs.gqlgen/generated/graph"
)

// TestCaseNewDefaultServer checks that a server created with
// handler.NewDefaultServer is traced.
type TestCaseNewDefaultServer struct {
	TestCase
}

func (tc *TestCaseNewDefaultServer) Setup(context.Context, *testing.T) {
	tc.server = handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
}
