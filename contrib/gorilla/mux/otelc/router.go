// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build ignore

// This file is never compiled as part of this package. The add_file rule in
// otelc.yaml copies it into github.com/gorilla/mux during an otelc build, where
// Router, RouteMatch and the injected Router.__dd_config field exist. otelc
// rewrites the package clause and drops the build constraint on the way in.
//
// It is the code of ../orchestrion.yml, kept as close to it as possible. Avoid
// generics and other recent language features: the code compiles with the
// language version of the gorilla/mux module in use.
package otelc

import (
	"math"
	"net/http"
	"sync"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	instrhttptrace "github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

var __dd_instr *instrumentation.Instrumentation

func init() {
	__dd_instr = instrumentation.Load(instrumentation.PackageGorillaMux)
}

type __dd_routerConfig struct {
	ignoreRequest func(*http.Request) bool
	headerTags    instrumentation.HeaderTags
	resourceNamer func(*Router, *http.Request) string
	serviceName   string
	spanOpts      []tracer.StartSpanOption
	initOnce      *sync.Once
	otelEnabled   bool
}

func __dd_defaultResourceNamer(router *Router, req *http.Request) string {
	var (
		match RouteMatch
		route = "unknown"
	)
	if router.Match(req, &match) && match.Route != nil {
		if r, err := match.Route.GetPathTemplate(); err == nil {
			route = r
		}
	}
	return req.Method + " " + route
}

// __dd_serveHTTP traces and serves the request, and reports whether it did.
// When it returns false, ServeHTTP serves the request itself.
func (r *Router) __dd_serveHTTP(w http.ResponseWriter, req *http.Request) bool {
	// Initialize on the first request, so subrouters get a configuration too.
	if r.__dd_config.initOnce == nil {
		r.__dd_config.initOnce = &sync.Once{}
	}
	r.__dd_config.initOnce.Do(func() {
		analyticsRate := __dd_instr.AnalyticsRate(true)
		r.__dd_config.headerTags = __dd_instr.HTTPHeadersAsTags()
		r.__dd_config.serviceName = __dd_instr.ServiceName(instrumentation.ComponentServer, nil)
		r.__dd_config.otelEnabled = __dd_instr.OTelSemanticsEnabled()
		// Leave resourceNamer nil so TraceAndServe applies default OpenTelemetry naming.
		if !r.__dd_config.otelEnabled {
			r.__dd_config.resourceNamer = __dd_defaultResourceNamer
		}
		r.__dd_config.ignoreRequest = func(_ *http.Request) bool { return false }

		r.__dd_config.spanOpts = []tracer.StartSpanOption{
			tracer.Tag(ext.Component, instrumentation.PackageGorillaMux),
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		}
		if !math.IsNaN(analyticsRate) {
			r.__dd_config.spanOpts = append(r.__dd_config.spanOpts, tracer.Tag(ext.EventSampleRate, analyticsRate))
		}
	})

	if r.__dd_config.ignoreRequest(req) {
		return false
	}

	var (
		match    RouteMatch
		route    string
		spanOpts = append([]tracer.StartSpanOption(nil), r.__dd_config.spanOpts...)
	)
	if r.Match(req, &match) && match.Route != nil {
		if h, err := match.Route.GetHostTemplate(); err == nil {
			spanOpts = append(spanOpts, tracer.Tag("mux.host", h))
		}
		route, _ = match.Route.GetPathTemplate()
	}
	spanOpts = append(spanOpts, instrhttptrace.HeaderTagsFromRequest(req, r.__dd_config.headerTags))
	var resource string
	if r.__dd_config.resourceNamer != nil {
		resource = r.__dd_config.resourceNamer(r, req)
	}

	// TraceAndServe calls back into ServeHTTP. Pass it a shallow copy of the
	// router that ignores every request, so the call does not recurse.
	rCopy := *r
	rCopy.__dd_config.ignoreRequest = func(*http.Request) bool { return true }

	httptrace.TraceAndServe(&rCopy, w, req, &httptrace.ServeConfig{
		Service:              r.__dd_config.serviceName,
		Resource:             resource,
		SpanOpts:             spanOpts,
		RouteParams:          match.Vars,
		Route:                route,
		RouteSet:             true,
		OTelSemanticsEnabled: &r.__dd_config.otelEnabled,
	})
	return true
}
