// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler/testserver"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestOptions(t *testing.T) {
	query := `{ name }`
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(assert *assert.Assertions, root mocktracer.Span)
	}{
		"default": {
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal("graphql.query", root.OperationName())
				assert.Equal(query, root.Tag(ext.ResourceName))
				assert.Equal(defaultServiceName, root.Tag(ext.ServiceName))
				assert.Equal(ext.SpanTypeGraphQL, root.Tag(ext.SpanType))
				assert.Equal("gqlgen", root.Tag(ext.Component))
				assert.Nil(root.Tag(ext.EventSampleRate))
			},
		},
		"WithServiceName": {
			tracerOpts: []Option{WithServiceName("TestServer")},
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal("TestServer", root.Tag(ext.ServiceName))
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
			c := newTestClient(t, testserver.New(), NewTracer(tt.tracerOpts...))
			var resp struct {
				Name string
			}
			c.MustPost(query, &resp)
			var root mocktracer.Span
			for _, span := range mt.FinishedSpans() {
				if span.ParentID() == 0 {
					root = span
				}
			}
			assert.NotNil(root)
			tt.test(assert, root)
			assert.Nil(root.Tag(ext.Error))
		})
	}
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := newTestClient(t, testserver.NewError(), NewTracer())
	var resp struct {
		Name string
	}
	err := c.Post(`{ name }`, &resp)
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
	var resp struct {
		Name string
	}
	query := `{ name }`
	err := c.Post(query, &resp)
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
		assert.Equal("gqlgen", span.Tag(ext.Component))
	}
	assert.ElementsMatch(resNames, []string{readOp, validationOp, parsingOp, query})
	assert.ElementsMatch(opNames, []string{readOp, validationOp, parsingOp, "graphql.query"})
	assert.NotNil(root)
	assert.Nil(root.Tag(ext.Error))
}

func newTestClient(t *testing.T, h *testserver.TestServer, tracer graphql.HandlerExtension) *client.Client {
	t.Helper()
	h.AddTransport(transport.POST{})
	h.Use(tracer)
	return client.New(h)
}
