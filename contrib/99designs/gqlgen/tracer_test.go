// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	mocktracer "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracerv2"
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/testserver"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

type testServerResponse struct {
	Name string
}

func TestOptions(t *testing.T) {
	query := `{ name }`
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(*assert.Assertions, mocktracer.Span, []mocktracer.Span)
		wantSpans  int
	}{
		"default": {
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal("graphql.query", root.Name)
				assert.Equal(query, root.Resource, root)
				assert.Equal("graphql", root.Service)
				assert.Equal(ext.SpanTypeGraphQL, root.Type)
				assert.Equal("99designs/gqlgen", root.Meta[ext.Component])
				assert.NotContains(root.Metrics, ext.EventSampleRate)
				assert.Equal(string(componentName), root.Meta[ext.Component])
			},
			wantSpans: 5,
		},
		"WithService": {
			tracerOpts: []Option{WithService("TestServer")},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal("TestServer", root.Service)
			},
			wantSpans: 5,
		},
		"WithAnalytics/true": {
			tracerOpts: []Option{WithAnalytics(true)},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal(1.0, root.Metrics[ext.EventSampleRate])
			},
			wantSpans: 5,
		},
		"WithAnalytics/false": {
			tracerOpts: []Option{WithAnalytics(false)},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.NotContains(root.Metrics, ext.EventSampleRate)
			},
			wantSpans: 5,
		},
		"WithAnalyticsRate": {
			tracerOpts: []Option{WithAnalyticsRate(0.5)},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal(0.5, root.Metrics[ext.EventSampleRate])
			},
			wantSpans: 5,
		},
		"WithoutTraceTrivialResolvedFields": {
			tracerOpts: []Option{WithoutTraceTrivialResolvedFields()},
			test: func(assert *assert.Assertions, _ mocktracer.Span, spans []mocktracer.Span) {
				var hasFieldOperation bool
				for _, span := range spans {
					if span.Name == fieldOp {
						hasFieldOperation = true
						break
					}
				}
				assert.Equal(false, hasFieldOperation)
			},
			wantSpans: 4,
		},
		"WithCustomTag": {
			tracerOpts: []Option{
				WithCustomTag("customTag1", "customValue1"),
				WithCustomTag("customTag2", "customValue2"),
			},
			test: func(assert *assert.Assertions, root mocktracer.Span, _ []mocktracer.Span) {
				assert.Equal("customValue1", root.Meta["customTag1"])
				assert.Equal("customValue2", root.Meta["customTag2"])
			},
			wantSpans: 5,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start(t)
			defer mt.Stop()
			c := newTestClient(t, testserver.New(), NewTracer(tt.tracerOpts...))
			c.MustPost(query, &testServerResponse{})
			spans := mt.WaitForSpans(t, tt.wantSpans)
			var root mocktracer.Span
			for _, span := range spans {
				if span.ParentID == 0 {
					root = span
				}
			}
			assert.NotNil(root)
			tt.test(assert, root, spans)
			assert.NotContains(root.Meta, ext.ErrorMsg)
		})
	}

	// WithoutTraceIntrospectionQuery tested here since we are specifically checking against an IntrosepctionQuery operation.
	query = `query IntrospectionQuery { __schema { queryType { name } } }`
	testFunc := func(assert *assert.Assertions, spans []mocktracer.Span) {
		var hasFieldSpan bool
		for _, span := range spans {
			if span.Name == fieldOp {
				hasFieldSpan = true
				break
			}
		}
		assert.Equal(false, hasFieldSpan)
	}
	for name, tt := range map[string]struct {
		tracerOpts []Option
		clientOpts []client.Option
		test       func(assert *assert.Assertions, spans []mocktracer.Span)
		wantSpans  int
	}{
		"WithoutTraceIntrospectionQuery with OperationName": {
			tracerOpts: []Option{WithoutTraceIntrospectionQuery()},
			test:       testFunc,
			clientOpts: []client.Option{client.Operation("IntrospectionQuery")},
			wantSpans:  4,
		},
		"WithoutTraceIntrospectionQuery without OperationName": {
			tracerOpts: []Option{WithoutTraceIntrospectionQuery()},
			clientOpts: []client.Option{},
			test:       testFunc,
			wantSpans:  4,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start(t)
			defer mt.Stop()
			c := newTestClient(t, testserver.New(), NewTracer(tt.tracerOpts...))
			c.MustPost(query, &testServerResponse{}, tt.clientOpts...)
			tt.test(assert, mt.WaitForSpans(t, tt.wantSpans))
		})
	}
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start(t)
	defer mt.Stop()
	c := newTestClient(t, testserver.NewError(), NewTracer())
	err := c.Post(`{ name }`, &testServerResponse{})
	assert.NotNil(err)
	var root mocktracer.Span
	for _, span := range mt.WaitForSpans(t, 4) {
		if span.ParentID == 0 {
			root = span
		}
	}
	assert.NotEmpty(root)
	assert.NotEmpty(root.Meta[ext.ErrorMsg])
}

func TestObfuscation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start(t)
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
	for _, span := range mt.WaitForSpans(t, 5) {
		assert.NotEmpty(t, span.Resource)
		assert.NotContains(span.Resource, "12345")
	}
}

func TestChildSpans(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start(t)
	defer mt.Stop()
	c := newTestClient(t, testserver.New(), NewTracer())
	err := c.Post(`{ name }`, &testServerResponse{})
	assert.Nil(err)
	var root mocktracer.Span
	allSpans := mt.WaitForSpans(t, 5)
	var resNames []string
	var opNames []string
	for _, span := range allSpans {
		if span.ParentID == 0 {
			root = span
		}
		resNames = append(resNames, span.Resource)
		opNames = append(opNames, span.Name)
		assert.Equal("99designs/gqlgen", span.Meta[ext.Component])
	}
	assert.ElementsMatch(resNames, []string{readOp, parsingOp, validationOp, "Query.name", `{ name }`})
	assert.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp, fieldOp, "graphql.query"})
	assert.NotEmpty(root)
	assert.NotContains(root.Meta, ext.ErrorMsg)
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
		mt := mocktracer.Start(t)

		err := c.Post(`{ name }`, &testServerResponse{})
		require.NoError(t, err)

		spans := mt.WaitForSpans(t, 5)

		var root mocktracer.Span
		var resNames []string
		var opNames []string
		for _, span := range spans {
			if span.ParentID == 0 {
				root = span
			}
			resNames = append(resNames, span.Resource)
			opNames = append(opNames, span.Name)
			assert.Equal(t, "99designs/gqlgen", span.Meta[ext.Component])
		}
		assert.ElementsMatch(t, resNames, []string{readOp, parsingOp, validationOp, "Query.name", `{ name }`})
		assert.ElementsMatch(t, opNames, []string{readOp, parsingOp, validationOp, fieldOp, "graphql.query"})
		assert.NotNil(t, root)
		assert.NotContains(t, root.Meta, ext.ErrorMsg)
	})

	t.Run("intercept operation with graphQL Mutation", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start(t)
		defer mt.Stop()

		err := c.Post(`mutation Name { name }`, &testServerResponse{})
		// due to testserver.New() implementation, mutation is not supported
		assert.NotNil(err)

		allSpans := mt.WaitForSpans(t, 4)
		var root mocktracer.Span
		var resNames []string
		var opNames []string
		for _, span := range allSpans {
			if span.ParentID == 0 {
				root = span
			}
			resNames = append(resNames, span.Resource)
			opNames = append(opNames, span.Name)
			assert.Equal("99designs/gqlgen", span.Meta[ext.Component])
		}
		assert.ElementsMatch(resNames, []string{readOp, parsingOp, validationOp, `mutation Name { name }`})
		assert.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp, "graphql.mutation"})
		assert.NotEmpty(root)
		assert.NotEmpty(root.Meta[ext.ErrorMsg])
	})

	t.Run("intercept operation with graphQL Subscription", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start(t)
		defer mt.Stop()

		go func() {
			graphqlTestSrv.SendCompleteSubscriptionMessage()
		}()

		// using raw post because post try to access nil response's Data field
		resp, err := c.RawPost(`subscription Name { name }`)
		assert.Nil(err)
		assert.Nil(resp)

		allSpans := mt.WaitForSpans(t, 3)
		var root mocktracer.Span
		var resNames []string
		var opNames []string
		for _, span := range allSpans {
			if span.ParentID == 0 {
				root = span
			}
			resNames = append(resNames, span.Resource)
			opNames = append(opNames, span.Name)
			assert.Equal("99designs/gqlgen", span.Meta[ext.Component])
		}
		assert.ElementsMatch(resNames, []string{`subscription Name { name }`, `subscription Name { name }`, "subscription Name { name }"})
		assert.ElementsMatch(opNames, []string{readOp, parsingOp, validationOp})
		assert.NotEmpty(root)
		assert.NotContains(root.Meta, ext.ErrorMsg)
	})
}
