// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

import (
	"net/http"
	"sync"

	v2 "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	v2tracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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
type ServeConfig = v2.ServeConfig

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
	c.FinishOpts = cfg.FinishOpts
	c.SpanOpts = cfg.SpanOpts
	c.IsStatusError = cfg.IsStatusError
	v2.TraceAndServe(h, w, r, c)
}
