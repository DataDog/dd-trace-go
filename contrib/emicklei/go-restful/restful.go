// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package restful provides functions to trace the emicklei/go-restful package (https://github.com/emicklei/go-restful).
package restful

import (
	"math"
	"strconv"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/emicklei/go-restful"
)

// FilterFunc returns a restful.FilterFunction which will automatically trace incoming request.
func FilterFunc(configOpts ...Option) restful.FilterFunction {
	cfg := newConfig()
	for _, opt := range configOpts {
		opt(cfg)
	}
	log.Debug("contrib/emicklei/go-restful: Creating tracing filter: %#v", cfg)
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		var opt ddtrace.StartSpanOption
		if !math.IsNaN(cfg.analyticsRate) {
			opt = tracer.Tag(ext.EventSampleRate, cfg.analyticsRate)
		}
		span, ctx := httptrace.StartRequestSpan(req.Request, cfg.serviceName, req.SelectedRoutePath(), false, opt)
		defer func() {
			httptrace.FinishRequestSpan(span, resp.StatusCode(), tracer.WithError(resp.Error()))
		}()

		// pass the span through the request context
		req.Request = req.Request.WithContext(ctx)
		chain.ProcessFilter(req, resp)
	}
}

// Filter is deprecated. Please use FilterFunc.
func Filter(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
	opts := []ddtrace.StartSpanOption{
		tracer.ResourceName(req.SelectedRoutePath()),
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.Tag(ext.HTTPMethod, req.Request.Method),
		tracer.Tag(ext.HTTPURL, req.Request.URL.Path),
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(req.Request.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	span, ctx := tracer.StartSpanFromContext(req.Request.Context(), "http.request", opts...)
	defer span.Finish()

	// pass the span through the request context
	req.Request = req.Request.WithContext(ctx)

	chain.ProcessFilter(req, resp)

	span.SetTag(ext.HTTPCode, strconv.Itoa(resp.StatusCode()))
	span.SetTag(ext.Error, resp.Error())
}
