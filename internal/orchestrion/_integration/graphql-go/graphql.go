// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	"github.com/stretchr/testify/require"
)

type TestCase struct {
	server *httptest.Server
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
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
		},
		),
	})
	require.NoError(t, err)

	tc.server = httptest.NewServer(handler.New(&handler.Config{Schema: &schema}))
	t.Cleanup(func() { tc.server.Close() })
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	req, err := http.NewRequest("POST", tc.server.URL, bytes.NewReader([]byte(`{"query": "{ hello }"}`)))
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer req.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var res struct {
		Data struct {
			Hello string
		}
	}
	require.NoError(t, json.Unmarshal(body, &res))
	require.Equal(t, "Hello, world!", res.Data.Hello)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "http.request",
				"type": "http",
			},
			Meta: map[string]string{
				"http.method":      "POST",
				"http.status_code": "200",
				"span.kind":        "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "POST /",
						"type":     "web",
					},
					Meta: map[string]string{
						"http.method":      "POST",
						"http.status_code": "200",
						"span.kind":        "server",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "graphql.server",
								"resource": "graphql.server",
								"service":  "graphql.server",
								"type":     "graphql",
							},
							Meta: map[string]string{
								"component": "graphql-go/graphql",
								"span.kind": "server",
							},
							Children: trace.Traces{
								{
									Tags: map[string]any{
										"name":     "graphql.parse",
										"resource": "graphql.parse",
										"service":  "graphql.server",
										"type":     "graphql",
									},
									Meta: map[string]string{
										"component": "graphql-go/graphql",
										"span.kind": "server",
									},
								},
								{
									Tags: map[string]any{
										"name":     "graphql.validate",
										"resource": "graphql.validate",
										"service":  "graphql.server",
										"type":     "graphql",
									},
									Meta: map[string]string{
										"component":      "graphql-go/graphql",
										"graphql.source": "{ hello }",
										"span.kind":      "server",
									},
								},
								{
									Tags: map[string]any{
										"name":     "graphql.execute",
										"resource": "graphql.execute",
										"service":  "graphql.server",
										"type":     "graphql",
									},
									Meta: map[string]string{
										"component":      "graphql-go/graphql",
										"graphql.source": "{ hello }",
										"span.kind":      "server",
									},
									Children: trace.Traces{
										{
											Tags: map[string]any{
												"name":     "graphql.resolve",
												"resource": "Query.hello",
												"service":  "graphql.server",
												"type":     "graphql",
											},
											Meta: map[string]string{
												"component":              "graphql-go/graphql",
												"graphql.field":          "hello",
												"graphql.operation.type": "query",
												"span.kind":              "server",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
