// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"context"
	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/testserver"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"

	internaltestserver "github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2/internal/testserver"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

type testServerResponse struct {
	Name string
}

func TestOptions(t *testing.T) {
	query := `{ name }`
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(*assert.Assertions, *mocktracer.Span, []*mocktracer.Span)
	}{
		"default": {
			test: func(assert *assert.Assertions, root *mocktracer.Span, _ []*mocktracer.Span) {
				assert.Equal("graphql.query", root.OperationName())
				assert.Equal(query, root.Tag(ext.ResourceName))
				assert.Equal("graphql", root.Tag(ext.ServiceName))
				assert.Equal(ext.SpanTypeGraphQL, root.Tag(ext.SpanType))
				assert.Equal("99designs/gqlgen", root.Tag(ext.Component))
				assert.Nil(root.Tag(ext.EventSampleRate))
				assert.Equal(string(componentName), root.Integration())
			},
		},
		"WithService": {
			tracerOpts: []Option{WithService("TestServer")},
			test: func(assert *assert.Assertions, root *mocktracer.Span, _ []*mocktracer.Span) {
				assert.Equal("TestServer", root.Tag(ext.ServiceName))
			},
		},
		"WithAnalytics/true": {
			tracerOpts: []Option{WithAnalytics(true)},
			test: func(assert *assert.Assertions, root *mocktracer.Span, _ []*mocktracer.Span) {
				assert.Equal(1.0, root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalytics/false": {
			tracerOpts: []Option{WithAnalytics(false)},
			test: func(assert *assert.Assertions, root *mocktracer.Span, _ []*mocktracer.Span) {
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalyticsRate": {
			tracerOpts: []Option{WithAnalyticsRate(0.5)},
			test: func(assert *assert.Assertions, root *mocktracer.Span, _ []*mocktracer.Span) {
				assert.Equal(0.5, root.Tag(ext.EventSampleRate))
			},
		},
		"WithoutTraceTrivialResolvedFields": {
			tracerOpts: []Option{WithoutTraceTrivialResolvedFields()},
			test: func(assert *assert.Assertions, _ *mocktracer.Span, spans []*mocktracer.Span) {
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
			test: func(assert *assert.Assertions, root *mocktracer.Span, _ []*mocktracer.Span) {
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
			var root *mocktracer.Span
			for _, span := range spans {
				if span.ParentID() == 0 {
					root = span
				}
			}
			assert.NotNil(root)
			tt.test(assert, root, spans)
			assert.Nil(root.Tag(ext.ErrorMsg))
		})
	}

	// WithoutTraceIntrospectionQuery tested here since we are specifically checking against an IntrosepctionQuery operation.
	query = `query IntrospectionQuery { __schema { queryType { name } } }`
	testFunc := func(assert *assert.Assertions, spans []*mocktracer.Span) {
		var hasFieldSpan bool
		for _, span := range spans {
			if span.OperationName() == fieldOp {
				hasFieldSpan = true
				break
			}
		}
		assert.Equal(false, hasFieldSpan)
	}
	for name, tt := range map[string]struct {
		tracerOpts []Option
		clientOpts []client.Option
		test       func(assert *assert.Assertions, spans []*mocktracer.Span)
	}{
		"WithoutTraceIntrospectionQuery with OperationName": {
			tracerOpts: []Option{WithoutTraceIntrospectionQuery()},
			test:       testFunc,
			clientOpts: []client.Option{client.Operation("IntrospectionQuery")},
		},
		"WithoutTraceIntrospectionQuery without OperationName": {
			tracerOpts: []Option{WithoutTraceIntrospectionQuery()},
			clientOpts: []client.Option{},
			test:       testFunc,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			c := newTestClient(t, testserver.New(), NewTracer(tt.tracerOpts...))
			c.MustPost(query, &testServerResponse{}, tt.clientOpts...)
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
	var root *mocktracer.Span
	for _, span := range mt.FinishedSpans() {
		if span.ParentID() == 0 {
			root = span
		}
	}
	require.NotNil(t, root)
	assert.NotNil(root.Tag(ext.ErrorMsg))

	events := root.Events()
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal("dd.graphql.query.error", evt.Name)
	assert.NotEmpty(evt.TimeUnixNano)
	assert.NotEmpty(evt.Attributes["stacktrace"])
	assert.Equal(map[string]any{
		"message":    "resolver error",
		"stacktrace": evt.Attributes["stacktrace"],
		"type":       "*gqlerror.Error",
	}, evt.Attributes)
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
	var root *mocktracer.Span
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
	assert.Zero(root.Tag(ext.ErrorMsg))
}

func newTestClient(t *testing.T, h *testserver.TestServer, tracer graphql.HandlerExtension) *client.Client {
	t.Helper()
	h.AddTransport(transport.POST{})
	h.Use(tracer)
	return client.New(h)
}

func TestInterceptOperation(t *testing.T) {
	graphqlTestSrv := testserver.New()
	c := newTestClient(t, graphqlTestSrv, NewTracer())

	t.Run("intercept operation with graphQL Query", func(t *testing.T) {
		assertions := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		err := c.Post(`{ name }`, &testServerResponse{})
		assertions.Nil(err)

		allSpans := mt.FinishedSpans()
		var root mocktracer.Span
		var resNames []string
		var opNames []string
		for _, span := range allSpans {
			if span.ParentID() == 0 {
				root = *span
			}
			resNames = append(resNames, span.Tag(ext.ResourceName).(string))
			opNames = append(opNames, span.OperationName())
			assertions.Equal("99designs/gqlgen", span.Tag(ext.Component))
		}
		assertions.ElementsMatch(resNames, []string{readOp, parsingOp, validationOp, "Query.name", `{ name }`})
		assertions.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp, fieldOp, "graphql.query"})
		assertions.NotNil(root)
		assertions.Nil(root.Tag(ext.ErrorMsg))
	})

	t.Run("intercept operation with graphQL Mutation", func(t *testing.T) {
		assertions := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		err := c.Post(`mutation Name { name }`, &testServerResponse{})
		// due to testserver.New() implementation, mutation is not supported
		assertions.NotNil(err)

		allSpans := mt.FinishedSpans()
		var root mocktracer.Span
		var resNames []string
		var opNames []string
		for _, span := range allSpans {
			if span.ParentID() == 0 {
				root = *span
			}
			resNames = append(resNames, span.Tag(ext.ResourceName).(string))
			opNames = append(opNames, span.OperationName())
			assertions.Equal("99designs/gqlgen", span.Tag(ext.Component))
		}
		assertions.ElementsMatch(resNames, []string{readOp, parsingOp, validationOp, `mutation Name { name }`})
		assertions.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp, "graphql.mutation"})
		assertions.NotNil(root)
		assertions.NotNil(root.Tag(ext.ErrorMsg))
	})

	t.Run("intercept operation with graphQL Subscription", func(t *testing.T) {
		assertions := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		go func() {
			graphqlTestSrv.SendCompleteSubscriptionMessage()
		}()

		// using raw post because post try to access nil response's Data field
		resp, err := c.RawPost(`subscription Name { name }`)
		assertions.Nil(err)
		assertions.Nil(resp)

		allSpans := mt.FinishedSpans()
		var root mocktracer.Span
		var resNames []string
		var opNames []string
		for _, span := range allSpans {
			if span.ParentID() == 0 {
				root = *span
			}
			resNames = append(resNames, span.Tag(ext.ResourceName).(string))
			opNames = append(opNames, span.OperationName())
			assertions.Equal("99designs/gqlgen", span.Tag(ext.Component))
		}
		assertions.ElementsMatch(resNames, []string{`subscription Name { name }`, `subscription Name { name }`, "subscription Name { name }"})
		assertions.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp})
		assertions.NotNil(root)
		assertions.Nil(root.Tag(ext.ErrorMsg))
	})
}

func TestErrorsAsSpanEvents(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	_, c := internaltestserver.New(t, NewTracer(WithErrorExtensions("str", "float", "int", "bool", "slice", "unsupported_type_stringified")))
	err := c.Post(`{ withError }`, &testServerResponse{})
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 5)

	s0 := spans[4]
	assert.Equal(t, "graphql.query", s0.OperationName())
	assert.NotNil(t, s0.Tag(ext.ErrorMsg))

	events := s0.Events()
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "dd.graphql.query.error", evt.Name)
	assert.NotEmpty(t, evt.TimeUnixNano)
	assert.NotEmpty(t, evt.Attributes["stacktrace"])

	wantAttrs := map[string]any{
		"message":          "test error",
		"path":             []any{"withError"},
		"stacktrace":       evt.Attributes["stacktrace"],
		"type":             "*gqlerror.Error",
		"extensions.str":   "1",
		"extensions.int":   1,
		"extensions.float": 1.1,
		"extensions.bool":  true,
		"extensions.slice": []any{"1", "2"},
		"extensions.unsupported_type_stringified": "[1,\"foo\"]",
	}
	evt.AssertAttributes(t, wantAttrs)

	// the rest of the spans should not have span events
	for _, s := range spans {
		if s.OperationName() == "graphql.query" {
			continue
		}
		assert.Emptyf(t, s.Events(), "span %s should not have span events", s.OperationName())
	}
}

// Test the extension does not panic when something returns a nil response
func TestNilResponse(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	h, c := internaltestserver.New(t, nil)
	h.Use(&nilResponseExtension{})
	h.Use(NewTracer())

	resp, err := c.RawPost(`{ withError }`)
	require.NoError(t, err)
	require.Nil(t, resp)
}

type nilResponseExtension struct{}

func (n *nilResponseExtension) ExtensionName() string {
	return "NilResponse"
}

func (n *nilResponseExtension) Validate(_ graphql.ExecutableSchema) error {
	return nil
}

func (n *nilResponseExtension) InterceptResponse(_ context.Context, _ graphql.ResponseHandler) *graphql.Response {
	return nil
}
