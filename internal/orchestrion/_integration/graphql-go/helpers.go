// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphql

import (
	"context"
	"maps"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// spansPerQuery is the number of spans one traced query gives: server, parse,
// validate, execute and one resolve.
const spansPerQuery = 5

func helloConfig() graphql.SchemaConfig {
	return graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"hello": &graphql.Field{
					Name: "hello",
					Type: graphql.NewNonNull(graphql.String),
					Resolve: func(graphql.ResolveParams) (any, error) {
						return "Hello, world!", nil
					},
				},
			},
		}),
	}
}

// queryHello runs the hello query on schema in-process.
func queryHello(ctx context.Context, t *testing.T, schema graphql.Schema) {
	t.Helper()
	res := graphql.Do(graphql.Params{
		Schema:        schema,
		RequestString: "{ hello }",
		Context:       ctx,
	})
	require.Empty(t, res.Errors)
	require.Equal(t, map[string]any{"hello": "Hello, world!"}, res.Data)
}

func graphqlSpan(name, resource string, meta map[string]string, children trace.Traces) *trace.Trace {
	m := map[string]string{
		"component": "graphql-go/graphql",
		"span.kind": "server",
	}
	maps.Copy(m, meta)
	return &trace.Trace{
		Tags: map[string]any{
			"name":     name,
			"resource": resource,
			"service":  "graphql.server",
			"type":     "graphql",
		},
		Meta:     m,
		Children: children,
	}
}

// expectHelloTrace is the trace of one hello query run with graphql.Do.
func expectHelloTrace() trace.Traces {
	source := map[string]string{"graphql.source": "{ hello }"}
	return trace.Traces{
		graphqlSpan("graphql.server", "graphql.server", nil, trace.Traces{
			graphqlSpan("graphql.parse", "graphql.parse", nil, trace.Traces{
				graphqlSpan("graphql.validate", "graphql.validate", source, trace.Traces{
					graphqlSpan("graphql.execute", "graphql.execute", source, trace.Traces{
						graphqlSpan("graphql.resolve", "Query.hello", map[string]string{
							"graphql.field":          "hello",
							"graphql.operation.type": "query",
						}, nil),
					}),
				}),
			}),
		}),
	}
}
