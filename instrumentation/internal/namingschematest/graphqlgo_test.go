// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	graphqltrace "github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var graphqlGo = harness.TestCase{
	Name:     instrumentation.PackageGraphQLGoGraphQL,
	GenSpans: graphQLGoGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("graphql.server", 5),
		DDService:       harness.RepeatString(harness.TestDDService, 5),
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 5),
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 5)
		assert.Equal(t, "graphql.parse", spans[0].OperationName())
		assert.Equal(t, "graphql.validate", spans[1].OperationName())
		assert.Equal(t, "graphql.resolve", spans[2].OperationName())
		assert.Equal(t, "graphql.execute", spans[3].OperationName())
		assert.Equal(t, "graphql.server", spans[4].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 5)
		assert.Equal(t, "graphql.parse", spans[0].OperationName())
		assert.Equal(t, "graphql.validate", spans[1].OperationName())
		assert.Equal(t, "graphql.resolve", spans[2].OperationName())
		assert.Equal(t, "graphql.execute", spans[3].OperationName())
		assert.Equal(t, "graphql.server", spans[4].OperationName())
	},
}

func graphQLGoGenSpans() harness.GenSpansFn {
	newTestServer := func(t *testing.T, opts ...graphqltrace.Option) *httptest.Server {
		cfg := graphql.SchemaConfig{
			Query: graphql.NewObject(graphql.ObjectConfig{
				Name: "Query",
				Fields: graphql.Fields{
					"withError": &graphql.Field{
						Args: graphql.FieldConfigArgument{},
						Type: graphql.ID,
						Resolve: func(_ graphql.ResolveParams) (interface{}, error) {
							return nil, errors.New("some error")
						},
					},
				},
			}),
		}
		schema, err := graphqltrace.NewSchema(cfg, opts...)
		require.NoError(t, err)

		h := handler.New(&handler.Config{Schema: &schema, Pretty: true, GraphiQL: true})
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)

		return srv
	}

	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []graphqltrace.Option
		if serviceOverride != "" {
			opts = append(opts, graphqltrace.WithService(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()

		srv := newTestServer(t, opts...)
		defer srv.Close()

		q := `{"query": "{ withError }"}`
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(q))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		return mt.FinishedSpans()
	}
}
