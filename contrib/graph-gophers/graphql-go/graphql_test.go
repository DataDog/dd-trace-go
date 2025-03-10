// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
	"github.com/stretchr/testify/assert"
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

func newTestServer(opts ...Option) *httptest.Server {
	schema := graphql.MustParseSchema(testServerSchema, new(testResolver), graphql.Tracer(NewTracer(opts...)))
	return httptest.NewServer(&relay.Handler{Schema: schema})
}

func Test(t *testing.T) {
	makeRequest := func(opts ...Option) {
		opts = append([]Option{WithServiceName("test-graphql-service")}, opts...)
		srv := newTestServer(opts...)
		defer srv.Close()
		q := `{"query": "query TestQuery() { hello, helloNonTrivial }", "operationName": "TestQuery"}`
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(q))
		assert.NoError(t, err)
		defer resp.Body.Close()
	}
	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		makeRequest()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3)
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())
		assert.Equal(t, spans[2].TraceID(), spans[0].TraceID())

		// The order of the spans isn't deterministic.
		helloSpanIndex := 0
		helloNonTrivialSpanIndex := 1
		if spans[0].Tag(tagGraphqlField) == "helloNonTrivial" {
			helloNonTrivialSpanIndex = 0
			helloSpanIndex = 1
		}
		{
			s := spans[helloNonTrivialSpanIndex]
			assert.Equal(t, "helloNonTrivial", s.Tag(tagGraphqlField))
			assert.Nil(t, s.Tag(ext.Error))
			assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
			assert.Equal(t, "Query", s.Tag(tagGraphqlType))
			assert.Equal(t, "graphql.field", s.OperationName())
			assert.Equal(t, "graphql.field", s.Tag(ext.ResourceName))
			assert.Equal(t, "graph-gophers/graphql-go", s.Tag(ext.Component))
			assert.Equal(t, "graph-gophers/graphql-go", s.Integration())
		}
		{
			s := spans[helloSpanIndex]
			assert.Equal(t, "hello", s.Tag(tagGraphqlField))
			assert.Nil(t, s.Tag(ext.Error))
			assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
			assert.Equal(t, "Query", s.Tag(tagGraphqlType))
			assert.Equal(t, "graphql.field", s.OperationName())
			assert.Equal(t, "graphql.field", s.Tag(ext.ResourceName))
			assert.Equal(t, "graph-gophers/graphql-go", s.Tag(ext.Component))
			assert.Equal(t, "graph-gophers/graphql-go", s.Integration())
		}
		{
			s := spans[2]
			assert.Equal(t, "query TestQuery() { hello, helloNonTrivial }", s.Tag(tagGraphqlQuery))
			assert.Equal(t, "TestQuery", s.Tag(tagGraphqlOperationName))
			assert.Nil(t, s.Tag(ext.Error))
			assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
			assert.Equal(t, "graphql.request", s.OperationName())
			assert.Equal(t, "graphql.request", s.Tag(ext.ResourceName))
			assert.Equal(t, "graph-gophers/graphql-go", s.Tag(ext.Component))
			assert.Equal(t, "graph-gophers/graphql-go", s.Integration())
		}
	})
	t.Run("WithOmitTrivial", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		makeRequest(WithOmitTrivial())

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID())
		{
			s := spans[0]
			assert.Equal(t, "helloNonTrivial", s.Tag(tagGraphqlField))
			assert.Nil(t, s.Tag(ext.Error))
			assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
			assert.Equal(t, "Query", s.Tag(tagGraphqlType))
			assert.Equal(t, "graphql.field", s.OperationName())
			assert.Equal(t, "graphql.field", s.Tag(ext.ResourceName))
			assert.Equal(t, "graph-gophers/graphql-go", s.Tag(ext.Component))
			assert.Equal(t, "graph-gophers/graphql-go", s.Integration())
		}
		{
			s := spans[1]
			assert.Equal(t, "query TestQuery() { hello, helloNonTrivial }", s.Tag(tagGraphqlQuery))
			assert.Equal(t, "TestQuery", s.Tag(tagGraphqlOperationName))
			assert.Nil(t, s.Tag(ext.Error))
			assert.Equal(t, "test-graphql-service", s.Tag(ext.ServiceName))
			assert.Equal(t, "graphql.request", s.OperationName())
			assert.Equal(t, "graphql.request", s.Tag(ext.ResourceName))
			assert.Equal(t, "graph-gophers/graphql-go", s.Tag(ext.Component))
			assert.Equal(t, "graph-gophers/graphql-go", s.Integration())
		}
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		srv := newTestServer(opts...)
		defer srv.Close()

		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"query": "{ hello }"}`))
		assert.NoError(t, err)
		defer resp.Body.Close()

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
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)

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

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
