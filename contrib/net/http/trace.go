// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

import (
	"net/http"
	"sync"

	v2 "github.com/DataDog/dd-trace-go/v2/contrib/net/http"
	v2tracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	serveConfigPool = sync.Pool{
		New: func() interface{} {
			return &v2.ServeConfig{
				FinishOpts: make([]v2tracer.FinishOption, 1),
				SpanOpts:   make([]v2tracer.StartSpanOption, 1),
			}
		},
	}
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig struct {
	// Service specifies the service name to use. If left blank, the global service name
	// will be inherited.
	Service string
	// Resource optionally specifies the resource name for this request.
	Resource string
	// QueryParams should be true in order to append the URL query values to the  "http.url" tag.
	QueryParams bool
	// Route is the request matched route if any, or is empty otherwise
	Route string
	// RouteParams specifies framework-specific route parameters (e.g. for route /user/:id coming
	// in as /user/123 we'll have {"id": "123"}). This field is optional and is used for monitoring
	// by AppSec. It is only taken into account when AppSec is enabled.
	RouteParams map[string]string
	// FinishOpts specifies any options to be used when finishing the request span.
	FinishOpts []ddtrace.FinishOption
	// SpanOpts specifies any options to be applied to the request starting span.
	SpanOpts []ddtrace.StartSpanOption
}

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	c := serveConfigPool.Get().(*v2.ServeConfig)
	defer serveConfigPool.Put(c)
	c.Service = cfg.Service
	c.Resource = cfg.Resource
	c.QueryParams = cfg.QueryParams
	c.Route = cfg.Route
	c.RouteParams = cfg.RouteParams
	c.FinishOpts[0] = tracer.ApplyV1FinishOptions(cfg.FinishOpts...)
	c.SpanOpts[0] = tracer.ApplyV1Options(cfg.SpanOpts...)
	v2.TraceAndServe(h, w, r, c)
}
