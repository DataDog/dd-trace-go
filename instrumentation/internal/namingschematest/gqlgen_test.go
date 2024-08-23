// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/testserver"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gqlgentrace "github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var gqlgen = harness.TestCase{
	Name: instrumentation.Package99DesignsGQLGen,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		type testServerResponse struct {
			Name string
		}
		newTestClient := func(t *testing.T, h *testserver.TestServer, tracer graphql.HandlerExtension) *client.Client {
			t.Helper()
			h.AddTransport(transport.POST{})
			h.Use(tracer)
			return client.New(h)
		}

		var opts []gqlgentrace.Option
		if serviceOverride != "" {
			opts = append(opts, gqlgentrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		c := newTestClient(t, testserver.New(), gqlgentrace.NewTracer(opts...))
		err := c.Post(`{ name }`, &testServerResponse{})
		require.NoError(t, err)

		err = c.Post(`mutation { name }`, &testServerResponse{})
		require.ErrorContains(t, err, "mutations are not supported")

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("graphql", 9),
		DDService:       harness.RepeatString("graphql", 9),
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 9),
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 9)
		assert.Equal(t, "graphql.read", spans[0].OperationName())
		assert.Equal(t, "graphql.parse", spans[1].OperationName())
		assert.Equal(t, "graphql.validate", spans[2].OperationName())
		assert.Equal(t, "graphql.field", spans[3].OperationName())
		assert.Equal(t, "graphql.query", spans[4].OperationName())
		assert.Equal(t, "graphql.read", spans[5].OperationName())
		assert.Equal(t, "graphql.parse", spans[6].OperationName())
		assert.Equal(t, "graphql.validate", spans[7].OperationName())
		assert.Equal(t, "graphql.mutation", spans[8].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 9)
		assert.Equal(t, "graphql.read", spans[0].OperationName())
		assert.Equal(t, "graphql.parse", spans[1].OperationName())
		assert.Equal(t, "graphql.validate", spans[2].OperationName())
		assert.Equal(t, "graphql.field", spans[3].OperationName())
		assert.Equal(t, "graphql.server.request", spans[4].OperationName())
		assert.Equal(t, "graphql.read", spans[5].OperationName())
		assert.Equal(t, "graphql.parse", spans[6].OperationName())
		assert.Equal(t, "graphql.validate", spans[7].OperationName())
		assert.Equal(t, "graphql.server.request", spans[8].OperationName())
	},
}
