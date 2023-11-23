// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package restful provides functions to trace the emicklei/go-restful package (https://github.com/emicklei/go-restful).
package restful

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/emicklei/go-restful/v3"
)

const componentName = "emicklei/go-restful.v3"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/emicklei/go-restful/v3")
}

// FilterFunc returns a restful.FilterFunction which will automatically trace incoming request.
func FilterFunc(configOpts ...Option) restful.FilterFunction {
	cfg := newConfig()
	for _, opt := range configOpts {
		opt(cfg)
	}
	log.Debug("contrib/emicklei/go-restful/v3: Creating tracing filter: %#v", cfg)
	spanOpts := []ddtrace.StartSpanOption{tracer.ServiceName(cfg.serviceName)}
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		spanOpts := append(
			spanOpts,
			tracer.ResourceName(req.SelectedRoutePath()),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.HTTPRoute, req.SelectedRoutePath()),
		)
		if !math.IsNaN(cfg.analyticsRate) {
			spanOpts = append(spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		spanOpts = append(spanOpts, httptrace.HeaderTagsFromRequest(req.Request, cfg.headerTags))
		span, ctx := httptrace.StartRequestSpan(req.Request, spanOpts...)
		defer func() {
			httptrace.FinishRequestSpan(span, resp.StatusCode(), tracer.WithError(resp.Error()))
		}()

		// pass the span through the request context
		req.Request = req.Request.WithContext(ctx)
		chain.ProcessFilter(req, resp)
	}
}
