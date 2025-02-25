// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gqlgen

import (
	"context"
	"fmt"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/99designs.gqlgen/generated/graph"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/require"
)

type TestCase struct {
	server *handler.Server
}

func (tc *TestCase) Setup(context.Context, *testing.T) {
	schema := graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}})
	tc.server = handler.New(schema)
	tc.server.AddTransport(transport.POST{})
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	c := client.New(tc.server)

	const (
		topLevelAttack = "he protec"
		nestedAttack   = "he attac, but most importantly: he Tupac"
	)

	var resp map[string]any
	require.NoError(t, c.Post(`
		query TestQuery($topLevelId: String!, $nestedId: String!) {
			topLevel(id: $topLevelId) {
				nested(id: $nestedId)
			}
		}
		`,
		&resp,
		client.Var("topLevelId", topLevelAttack),
		client.Var("nestedId", nestedAttack),
		client.Operation("TestQuery"),
	))

	require.Equal(t, map[string]any{
		"topLevel": map[string]any{
			"nested": fmt.Sprintf("%s/%s", topLevelAttack, nestedAttack),
		},
	}, resp)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":    "graphql.query",
				"service": "graphql",
				"type":    "graphql",
			},
			Meta: map[string]string{
				"component": "99designs/gqlgen",
				"span.kind": "server",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "graphql.field",
						"service":  "graphql",
						"resource": "TopLevel.nested",
					},
					Meta: map[string]string{
						"component":              "99designs/gqlgen",
						"graphql.operation.type": "query",
						"graphql.field":          "nested",
					},
				},
				{
					Tags: map[string]any{
						"name":     "graphql.read",
						"service":  "graphql",
						"resource": "graphql.read",
					},
					Meta: map[string]string{
						"component": "99designs/gqlgen",
					},
				},
				{
					Tags: map[string]any{
						"name":     "graphql.parse",
						"service":  "graphql",
						"resource": "graphql.parse",
					},
					Meta: map[string]string{
						"component": "99designs/gqlgen",
					},
				},
				{
					Tags: map[string]any{
						"name":     "graphql.validate",
						"service":  "graphql",
						"resource": "graphql.validate",
					},
					Meta: map[string]string{
						"component": "99designs/gqlgen",
					},
				},
				{
					Tags: map[string]any{
						"name":     "graphql.field",
						"service":  "graphql",
						"resource": "Query.topLevel",
					},
					Meta: map[string]string{
						"component":              "99designs/gqlgen",
						"graphql.operation.type": "query",
						"graphql.field":          "topLevel",
					},
				},
			},
		},
	}
}
