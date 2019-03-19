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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
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

func TestAnalyticsSettings(t *testing.T) {
	s := `
		schema {
			query: Query
		}
		type Query {
			hello: String!
		}
	`

	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		schema := graphql.MustParseSchema(s, new(testResolver),
			graphql.Tracer(NewTracer(opts...)))
		srv := httptest.NewServer(&relay.Handler{Schema: schema})
		defer srv.Close()
		http.Post(srv.URL, "application/json", strings.NewReader(`{
			"query": "{ hello }"
		}`))

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)

		assert.Equal(t, rate, spans[0].Tag(ext.EventSampleRate))
		assert.Equal(t, rate, spans[1].Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
