package namingschematest

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/testserver"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	gqlgentrace "github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var gqlgen = testCase{
	name: instrumentation.Package99DesignsGQLGen,
	genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
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
	wantServiceNameV0: serviceNameAssertions{
		defaults:        repeatString("graphql", 9),
		ddService:       repeatString("graphql", 9),
		serviceOverride: repeatString(testServiceOverride, 9),
	},
	assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
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
	assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
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
