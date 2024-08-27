package namingschematest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	grahqltrace "github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type testResolver struct{}

func (*testResolver) Hello() string                    { return "Hello, world!" }
func (*testResolver) HelloNonTrivial() (string, error) { return "Hello, world!", nil }

const testServerSchema = `
	schema {
		query: Query
	}
	type Query {
		hello: String!
		helloNonTrivial: String!
	}
`

func newTestServer(opts ...grahqltrace.Option) *httptest.Server {
	schema := graphql.MustParseSchema(testServerSchema, new(testResolver), graphql.Tracer(grahqltrace.NewTracer(opts...)))
	return httptest.NewServer(&relay.Handler{Schema: schema})
}

func graphGophersGraphQLGoGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []grahqltrace.Option
		if serviceOverride != "" {
			opts = append(opts, grahqltrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		srv := newTestServer(opts...)
		defer srv.Close()
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"query": "{ hello }"}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		return mt.FinishedSpans()
	}
}

var graphGophersGraphQLGo = harness.TestCase{
	Name:     instrumentation.PackageGraphGophersGraphQLGo,
	GenSpans: graphGophersGraphQLGoGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("graphql", 2),
		DDService:       harness.RepeatString(harness.TestDDService, 2),
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 2),
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "graphql.field", spans[0].OperationName())
		assert.Equal(t, "graphql.request", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "graphql.field", spans[0].OperationName())
		assert.Equal(t, "graphql.server.request", spans[1].OperationName())
	},
}
