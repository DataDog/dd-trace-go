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
	for name, tt := range map[string]struct {
		tracerOpts []Option
		test       func(assert *assert.Assertions, root mocktracer.Span)
	}{
		"default": {
			test: func(assert *assert.Assertions, root mocktracer.Span) {
				assert.Equal(root.OperationName(), graphQLQuery)
				assert.Equal(root.Tag(ext.ResourceName), graphQLQuery)
				assert.Equal(root.Tag(ext.ServiceName), defaultServiceName)
				assert.Equal(root.Tag(ext.SpanType), ext.SpanTypeGraphQL)
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
			c.MustPost(`{ name }`, &resp)
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

func TestWithChildSpans(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	c := newTestClient(t, testserver.New(), NewTracer())
	var resp struct {
		Name string
	}
	err := c.Post(`{ name }`, &resp)
	assert.Nil(err)
	var root mocktracer.Span
	allSpans := mt.FinishedSpans()
	var foundOps []string
	for _, span := range allSpans {
		if span.ParentID() == 0 {
			root = span
		}
		foundOps = append(foundOps, span.Tag(ext.ResourceName).(string))
	}
	assert.ElementsMatch(foundOps, []string{readOp, validationOp, parsingOp, graphQLQuery})
	assert.NotNil(root)
	assert.Nil(root.Tag(ext.Error))
}

func newTestClient(t *testing.T, h *testserver.TestServer, tracer graphql.HandlerExtension) *client.Client {
	t.Helper()
	h.AddTransport(transport.POST{})
	h.Use(tracer)
	return client.New(h)
}
