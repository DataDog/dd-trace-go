// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package restful provides functions to trace the emicklei/go-restful package (https://github.com/emicklei/go-restful).
package restful

import (
	"math"

	"github.com/emicklei/go-restful/v3"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

const componentName = "emicklei/go-restful.v3"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageEmickleiGoRestfulV3)
}

// FilterFunc returns a restful.FilterFunction which will automatically trace incoming request.
func FilterFunc(configOpts ...Option) restful.FilterFunction {
	cfg := newConfig()
	for _, opt := range configOpts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/emicklei/go-restful/v3: Creating tracing filter: %#v", cfg)
	spanOpts := []tracer.StartSpanOption{instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource)}
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		route := req.SelectedRoutePath()
		var resource string
		if cfg.otelSemanticsEnabled {
			resource = httptrace.ServerSpanName(req.Request.Method, route)
		} else {
			resource = route
		}
		requestSpanOpts := append(
			spanOpts,
			tracer.ResourceName(resource),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		)
		if route != "" || !cfg.otelSemanticsEnabled {
			requestSpanOpts = append(requestSpanOpts, tracer.Tag(ext.HTTPRoute, route))
		}
		if cfg.otelSemanticsEnabled {
			requestSpanOpts = append(requestSpanOpts, httptrace.HTTPEndpointTag(route, req.Request))
		}
		if !math.IsNaN(cfg.analyticsRate) {
			requestSpanOpts = append(requestSpanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		requestSpanOpts = append(requestSpanOpts, httptrace.HeaderTagsFromRequest(req.Request, cfg.headerTags))
		_, ctx, finishSpans := httptrace.StartRequestSpan(req.Request, requestSpanOpts...)
		defer func() {
			finishSpans(resp.StatusCode(), nil, tracer.WithError(resp.Error()))
		}()

		// pass the span through the request context
		req.Request = req.Request.WithContext(ctx)
		chain.ProcessFilter(req, resp)
	}
}
