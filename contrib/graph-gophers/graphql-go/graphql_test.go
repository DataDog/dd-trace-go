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

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/lists"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testResolver struct{}

func (*testResolver) Hello() string                    { return "Hello, world!" }
func (*testResolver) HelloNonTrivial() (string, error) { return "Hello, world!", nil }
func (*testResolver) WithError() (*graphql.ID, error) {
	return nil, customError{
		message: "test error",
		extensions: map[string]any{
			"int":                          1,
			"float":                        1.1,
			"str":                          "1",
			"bool":                         true,
			"slice":                        []string{"1", "2"},
			"unsupported_type_stringified": []any{1, "foo"},
			"not_captured":                 "nope",
		},
	}
}

type customError struct {
	message    string
	extensions map[string]any
}

func (e customError) Error() string {
	return e.message
}

func (e customError) Extensions() map[string]any {
	return e.extensions
}

const testServerSchema = `
	schema {
		query: Query
	}
	type Query {
		hello: String!
		helloNonTrivial: String!
		withError: ID
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
			assert.Equal(t, componentName, s.Integration())
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
			assert.Equal(t, componentName, s.Integration())
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
			assert.Equal(t, componentName, s.Integration())
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
			assert.Equal(t, componentName, s.Integration())
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
			assert.Equal(t, componentName, s.Integration())
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

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		srv := newTestServer(opts...)
		defer srv.Close()
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"query": "{ hello }"}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "graphql.field", spans[0].OperationName())
		assert.Equal(t, "graphql.request", spans[1].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "graphql.field", spans[0].OperationName())
		assert.Equal(t, "graphql.server.request", spans[1].OperationName())
	}
	ddService := namingschematest.TestDDService
	serviceOverride := namingschematest.TestServiceOverride
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             lists.RepeatString("graphql.server", 2),
		WithDDService:            lists.RepeatString(ddService, 2),
		WithDDServiceAndOverride: lists.RepeatString(serviceOverride, 2),
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}

func TestErrorsAsSpanEvents(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(WithErrorExtensions("str", "float", "int", "bool", "slice", "unsupported_type_stringified"))
	defer srv.Close()

	q := `{"query": "{ withError }"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(q))
	require.NoError(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	s0 := spans[1]
	assert.Equal(t, "graphql.request", s0.OperationName())
	assert.NotNil(t, s0.Tag(ext.Error))

	events := s0.Events()
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "dd.graphql.query.error", evt.Name)
	assert.NotEmpty(t, evt.Config.Time)
	assert.NotEmpty(t, evt.Config.Attributes["stacktrace"])
	assert.Equal(t, map[string]any{
		"message":          "test error",
		"path":             []string{"withError"},
		"stacktrace":       evt.Config.Attributes["stacktrace"],
		"type":             "*errors.QueryError",
		"extensions.str":   "1",
		"extensions.int":   1,
		"extensions.float": 1.1,
		"extensions.bool":  true,
		"extensions.slice": []string{"1", "2"},
		"extensions.unsupported_type_stringified": "[1,\"foo\"]",
	}, evt.Config.Attributes)

	// the rest of the spans should not have span events
	for _, s := range spans {
		if s.OperationName() == "graphql.request" {
			continue
		}
		assert.Emptyf(t, s.Events(), "span %s should not have span events", s.OperationName())
	}
}
