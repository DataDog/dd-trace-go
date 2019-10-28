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

func newTodoClient(t graphql.Tracer) *client.Client {
	c := client.New(handler.GraphQL(
		todo.NewExecutableSchema(todo.New()),
		handler.Tracer(t),
	))
	return c
}

func TestOptions(t *testing.T) {
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(assert *assert.Assertions, root mocktracer.Span)
	}{
		"default": {
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal(root.OperationName(), spanGQLGenOperation)
				assert.Equal(root.Tag(ext.ServiceName), defaultServiceName)
				assert.Equal(root.Tag(ext.SpanType), ext.SpanTypeGraphQL)
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithServiceName": {
			tracerOpts: []Option{WithServiceName("TodoServer")},
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal("TodoServer", root.Tag(ext.ServiceName))
			},
		},
		"WithAnalytics/true": {
			tracerOpts: []Option{WithAnalytics(true)},
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal(1.0, root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalytics/false": {
			tracerOpts: []Option{WithAnalytics(false)},
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithAnalyticsRate": {
			tracerOpts: []Option{WithAnalyticsRate(0.5)},
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal(0.5, root.Tag(ext.EventSampleRate))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			c := newTodoClient(NewTracer(tt.tracerOpts...))

			var createResp struct {
				CreateTodo struct{ ID string }
			}
			err := c.Post(`mutation CreateTodo{ createTodo(todo: {text: "todo text"}) {id} }`, &createResp)
			if err != nil {
				t.Error(err)
				return
			}
			var root mocktracer.Span
			for _, span := range mt.FinishedSpans() {
				if span.ParentID() == 0 {
					root = span
				}
			}
			assert.NotNil(root)
			tt.test(assert, root)
		})
	}
}

func TestResolver(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := newTodoClient(NewTracer())

	var createResp struct {
		CreateTodo struct{ ID string }
	}
	operation := "CreateTodo"
	query := `mutation CreateTodo($text: String!){ createTodo(todo: {text: $text}) {id} }`
	todoText := "todo text"
	err := c.Post(query, &createResp, client.Var("text", todoText), client.Operation(operation))
	if err != nil {
		t.Error(err)
		return
	}

	spans := mt.FinishedSpans()
	var root, resolver, field mocktracer.Span
	for _, span := range spans {
		switch span.Tag(ext.ResourceName) {
		case operation:
			root = span
		case "MyMutation.createTodo":
			resolver = span
		case "Todo.id":
			field = span
		}
	}
	assert.NotNil(root)
	assert.Equal(spanGQLGenOperation, root.OperationName())
	assert.Equal(query, root.Tag(tagGraphQLQuery))
	assert.Equal(defaultServiceName, root.Tag(ext.ServiceName))
	assert.Equal(ext.SpanTypeGraphQL, root.Tag(ext.SpanType))
	assert.Equal(todoText, root.Tag("variables.text"))

	assert.NotNil(resolver)
	assert.Equal(spanGQLGenField, resolver.OperationName())
	assert.Equal(defaultServiceName, resolver.Tag(ext.ServiceName))
	assert.Equal(root.SpanID(), resolver.ParentID())

	assert.NotNil(field)
	assert.Equal(spanGQLGenField, field.OperationName())
	assert.Equal(defaultServiceName, field.Tag(ext.ServiceName))
	assert.Equal(resolver.SpanID(), field.ParentID())

}
