// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/lists"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/testserver"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testServerResponse struct {
	Name string
}

func TestOptions(t *testing.T) {
	query := `{ name }`
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(assert *assert.Assertions, root mocktracer.Span, spans []mocktracer.Span)
	}{
		"default": {
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal("graphql.query", root.OperationName())
				assert.Equal(query, root.Tag(ext.ResourceName))
				assert.Equal(defaultServiceName, root.Tag(ext.ServiceName))
				assert.Equal(ext.SpanTypeGraphQL, root.Tag(ext.SpanType))
				assert.Equal("99designs/gqlgen", root.Tag(ext.Component))
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithServiceName": {
			tracerOpts: []Option{WithServiceName("TestServer")},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal("TestServer", root.Tag(ext.ServiceName))
			},
		},
		"WithAnalytics/true": {
			tracerOpts: []Option{WithAnalytics(true)},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal(1.0, root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalytics/false": {
			tracerOpts: []Option{WithAnalytics(false)},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalyticsRate": {
			tracerOpts: []Option{WithAnalyticsRate(0.5)},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal(0.5, root.Tag(ext.EventSampleRate))
			},
		},
		"WithoutTraceTrivialResolvedFields": {
			tracerOpts: []Option{WithoutTraceTrivialResolvedFields()},
			test: func(assert *assert.Assertions, _ mocktracer.Span, spans []mocktracer.Span) {
				var hasFieldOperation bool
				for _, span := range spans {
					if span.OperationName() == fieldOp {
						hasFieldOperation = true
						break
					}
				}
				assert.Equal(false, hasFieldOperation)
			},
		},
		"WithCustomTag": {
			tracerOpts: []Option{
				WithCustomTag("customTag1", "customValue1"),
				WithCustomTag("customTag2", "customValue2"),
			},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal("customValue1", root.Tag("customTag1"))
				assert.Equal("customValue2", root.Tag("customTag2"))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			c := newTestClient(t, testserver.New(), NewTracer(tt.tracerOpts...))
			c.MustPost(query, &testServerResponse{})
			spans := mt.FinishedSpans()
			var root mocktracer.Span
			for _, span := range spans {
				if span.ParentID() == 0 {
					root = span
				}
			}
			assert.NotNil(root)
			tt.test(assert, root, spans)
			assert.Nil(root.Tag(ext.Error))
		})
	}

	// WithoutTraceIntrospectionQuery tested here since we are specifically checking against an IntrosepctionQuery operation.
	query = `query IntrospectionQuery { __schema { queryType { name } } }`
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(assert *assert.Assertions, spans []mocktracer.Span)
	}{
		"WithoutTraceIntrospectionQuery": {
			tracerOpts: []Option{WithoutTraceIntrospectionQuery()},
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				var hasFieldSpan bool
				for _, span := range spans {
					if span.OperationName() == fieldOp {
						hasFieldSpan = true
						break
					}
				}
				assert.Equal(false, hasFieldSpan)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			c := newTestClient(t, testserver.New(), NewTracer(tt.tracerOpts...))
			c.MustPost(query, &testServerResponse{}, client.Operation("IntrospectionQuery"))
			tt.test(assert, mt.FinishedSpans())
		})
	}
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := newTestClient(t, testserver.NewError(), NewTracer())
	err := c.Post(`{ name }`, &testServerResponse{})
	assert.NotNil(err)
	var root mocktracer.Span
	for _, span := range mt.FinishedSpans() {
		if span.ParentID() == 0 {
			root = span
		}
	}
	assert.NotNil(root)
	assert.NotNil(root.Tag(ext.Error))
}

func TestObfuscation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := newTestClient(t, testserver.New(), NewTracer())
	var resp struct {
		Name string
	}
	query := `query($id: Int!) {
	name
	find(id: $id)
}
`
	err := c.Post(query, &resp, client.Var("id", 12345))
	assert.Nil(err)

	// No spans should contain the sensitive ID.
	for _, span := range mt.FinishedSpans() {
		assert.NotContains(span.Tag(ext.ResourceName), "12345")
	}
}

func TestChildSpans(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := newTestClient(t, testserver.New(), NewTracer())
	err := c.Post(`{ name }`, &testServerResponse{})
	assert.Nil(err)
	var root mocktracer.Span
	allSpans := mt.FinishedSpans()
	var resNames []string
	var opNames []string
	for _, span := range allSpans {
		if span.ParentID() == 0 {
			root = span
		}
		resNames = append(resNames, span.Tag(ext.ResourceName).(string))
		opNames = append(opNames, span.OperationName())
		assert.Equal("99designs/gqlgen", span.Tag(ext.Component))
	}
	assert.ElementsMatch(resNames, []string{readOp, parsingOp, validationOp, "Query.name", `{ name }`})
	assert.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp, fieldOp, "graphql.query"})
	assert.NotNil(root)
	assert.Nil(root.Tag(ext.Error))
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		c := newTestClient(t, testserver.New(), NewTracer(opts...))
		err := c.Post(`{ name }`, &testServerResponse{})
		require.NoError(t, err)

		err = c.Post(`mutation Name { name }`, &testServerResponse{})
		assert.ErrorContains(t, err, "mutations are not supported")

		return mt.FinishedSpans()
	})
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
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
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
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
	}
	serviceOverride := namingschematest.TestServiceOverride
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             lists.RepeatString("graphql", 9),
		WithDDService:            lists.RepeatString("graphql", 9),
		WithDDServiceAndOverride: lists.RepeatString(serviceOverride, 9),
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}

func newTestClient(t *testing.T, h *testserver.TestServer, tracer graphql.HandlerExtension) *client.Client {
	t.Helper()
	h.AddTransport(transport.POST{})
	h.Use(tracer)
	return client.New(h)
}
