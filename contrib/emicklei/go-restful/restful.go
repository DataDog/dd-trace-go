// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package restful provides functions to trace the emicklei/go-restful package (https://github.com/emicklei/go-restful).
// WARNING: The underlying v2 version of emicklei/go-restful has known security vulnerabilities that have been resolved in v3
// and is no longer under active development. As such consider this package DEPRECATED.
// It is highly recommended that you update to the latest version available at emicklei/go-restful.v3.
package restful

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/emicklei/go-restful"
)

const componentName = "emicklei/go-restful"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/emicklei/go-restful")
}

// FilterFunc returns a restful.FilterFunction which will automatically trace incoming request.
func FilterFunc(configOpts ...Option) restful.FilterFunction {
	cfg := newConfig()
	for _, opt := range configOpts {
		opt(cfg)
	}
	log.Debug("contrib/emicklei/go-restful: Creating tracing filter: %#v", cfg)
	spanOpts := []ddtrace.StartSpanOption{tracer.ServiceName(cfg.serviceName)}
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		spanOpts := append(spanOpts, tracer.ResourceName(req.SelectedRoutePath()))
		spanOpts = append(spanOpts, tracer.Tag(ext.Component, componentName))
		spanOpts = append(spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))

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

// Filter is deprecated. Please use FilterFunc.
func Filter(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
	span, ctx := httptrace.StartRequestSpan(req.Request, tracer.ResourceName(req.SelectedRoutePath()))
	defer func() {
		httptrace.FinishRequestSpan(span, resp.StatusCode(), tracer.WithError(resp.Error()))
	}()

	// pass the span through the request context
	req.Request = req.Request.WithContext(ctx)
	chain.ProcessFilter(req, resp)
}
