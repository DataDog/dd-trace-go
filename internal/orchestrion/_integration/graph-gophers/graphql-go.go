// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package graphgophers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
	"github.com/stretchr/testify/require"
)

type TestCase struct {
	server *httptest.Server
}

const schema = `
	schema {
		query: Query
	}
	type Query {
		hello: String!
	}
`

type resolver struct{}

func (*resolver) Hello() string {
	return "Hello, world!"
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	schema, err := graphql.ParseSchema(schema, new(resolver))
	require.NoError(t, err)

	tc.server = httptest.NewServer(&relay.Handler{Schema: schema})
	t.Cleanup(func() { tc.server.Close() })
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, tc.server.URL, bytes.NewReader([]byte(`{"query": "{ hello }"}`)))
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer resp.Body.Close()

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
								"name":     "graphql.request",
								"resource": "graphql.request",
								"service":  "graphql.server",
							},
							Meta: map[string]string{
								"component":     "graph-gophers/graphql-go",
								"graphql.query": "{ hello }",
							},
							Children: trace.Traces{
								{
									Tags: map[string]any{
										"name":     "graphql.field",
										"resource": "graphql.field",
										"service":  "graphql.server",
									},
									Meta: map[string]string{
										"component":     "graph-gophers/graphql-go",
										"graphql.field": "hello",
										"graphql.type":  "Query",
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
