// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package gqlgen

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/example/todo"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/handler"
	"github.com/stretchr/testify/assert"
)

func TestImplementsTracer(t *testing.T) {
	var _ graphql.Tracer = (*gqlTracer)(nil)
}

func createTodoClient(t graphql.Tracer) *client.Client {

	c := client.New(handler.GraphQL(
		todo.NewExecutableSchema(todo.New()),
		handler.Tracer(t),
	))
	return c
}

// findRootSpan returns the first root span it finds in a slice of spans, or nil if none is found.
func findRootSpan(spans []mocktracer.Span) mocktracer.Span {
	for _, span := range spans {
		if span.ParentID() == 0 {
			return span
		}
	}
	return nil
}

// findRootSpan returns the first span it finds with a matching tag value in a slice of spans, or nil if none is found.
func findSpanWithTag(spans []mocktracer.Span, tag string, val interface{}) mocktracer.Span {
	for _, span := range spans {
		if span.Tag(tag) == val {
			return span
		}
	}
	return nil
}

func TestRootSpan(t *testing.T) {
	for name, tt := range map[string]struct {
		clientOpts []client.Option
		tracerOpts []Option
		test       func(assert *assert.Assertions, spans []mocktracer.Span)
	}{
		"Defaults": {
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				root := findRootSpan(spans)
				assert.NotNil(root)
				assert.Equal(root.Tag(ext.ResourceName), defaultResourceName)
				assert.Equal(root.Tag(ext.ServiceName), defaultServiceName)
				assert.Equal(root.Tag(ext.SpanType), ext.SpanTypeGraphql)
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"OperationName": {
			clientOpts: []client.Option{client.Operation("CreateTodo")},
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				root := findRootSpan(spans)
				assert.NotNil(root)
				assert.Equal(root.Tag(ext.ResourceName), "CreateTodo")
			},
		},
		"ServiceName": {
			tracerOpts: []Option{WithServiceName("TodoServer")},
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				root := findRootSpan(spans)
				assert.NotNil(root)
				assert.Equal(root.Tag(ext.ServiceName), "TodoServer")
			},
		},
		"WithAnalytics/true": {
			tracerOpts: []Option{WithAnalytics(true)},
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				root := findRootSpan(spans)
				assert.NotNil(root)
				assert.Equal(1.0, root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalytics/false": {
			tracerOpts: []Option{WithAnalytics(false)},
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				root := findRootSpan(spans)
				assert.NotNil(root)
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalyticsRate": {
			tracerOpts: []Option{WithAnalyticsRate(0.5)},
			test: func(assert *assert.Assertions, spans []mocktracer.Span) {
				root := findRootSpan(spans)
				assert.NotNil(root)
				assert.Equal(0.5, root.Tag(ext.EventSampleRate))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			c := createTodoClient(New(tt.tracerOpts...))

			var createResp struct {
				CreateTodo struct{ ID string }
			}
			err := c.Post(`mutation CreateTodo{ createTodo(todo: {text: "todo text"}) {id} }`, &createResp, tt.clientOpts...)
			if err != nil {
				t.Error(err)
				return
			}
			spans := mt.FinishedSpans()
			tt.test(assert, spans)
		})
	}
}

func TestResolver(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := createTodoClient(New())

	var createResp struct {
		CreateTodo struct{ ID string }
	}
	err := c.Post(`mutation CreateTodo{ createTodo(todo: {text: "todo text"}) {id} }`, &createResp)
	if err != nil {
		t.Error(err)
		return
	}
	spans := mt.FinishedSpans()
	span := findSpanWithTag(spans, ext.ResourceName, "MyMutation_createTodo")
	assert.NotNil(span)
	assert.Equal("MyMutation_createTodo", span.Tag(ext.SpanName))
	assert.Equal("MyMutation", span.Tag(resolverObject))
	assert.Equal("createTodo", span.Tag(resolverField))
}
