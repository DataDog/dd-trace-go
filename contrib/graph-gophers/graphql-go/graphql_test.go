package graphql

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graphql "github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

type testResolver struct{}

func (*testResolver) Hello() string { return "Hello, world!" }

func Test(t *testing.T) {
	s := `
		schema {
			query: Query
		}
		type Query {
			hello: String!
		}
	`
	schema := graphql.MustParseSchema(s, new(testResolver),
		graphql.Tracer(NewTracer(WithServiceName("test-graphql-service"))))
	srv := httptest.NewServer(&relay.Handler{Schema: schema})
	defer srv.Close()

	mt := mocktracer.Start()
	defer mt.Stop()

	http.Post(srv.URL, "application/json", strings.NewReader(`{
		"query": "{ hello }"
	}`))

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())

	{
		s := spans[0]
		assert.Equal(t, "hello", s.Tag(tagGraphqlField))
		assert.Nil(t, s.Tag(ext.Error))
		assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
		assert.Equal(t, "Query", s.Tag(tagGraphqlType))
		assert.Equal(t, "graphql.field", s.OperationName())
		assert.Equal(t, "graphql.field", s.Tag(ext.ResourceName))
	}

	{
		s := spans[1]
		assert.Equal(t, "{ hello }", s.Tag(tagGraphqlQuery))
		assert.Nil(t, s.Tag(ext.Error))
		assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
		assert.Equal(t, "graphql.request", s.OperationName())
		assert.Equal(t, "graphql.request", s.Tag(ext.ResourceName))
	}
}
