// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc hooks for contrib/gorilla/mux, the otelc port
// of contrib/gorilla/mux/orchestrion.yml. It is its own module because it
// imports go.opentelemetry.io/otelc/pkg/hook, which must not land in the
// go.mod of every contrib/gorilla/mux consumer.
package otelc

import (
	"math"
	"net/http"
	"sync"

	"github.com/gorilla/mux"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	instrhttptrace "github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"go.opentelemetry.io/otelc/pkg/hook"
)

var instr = instrumentation.Load(instrumentation.PackageGorillaMux)

// routerConfig is the per-Router state the orchestrion aspect keeps in an
// unexported __dd_config struct field. A hook can only read and write
// exported fields, so this lives here instead and is reached through
// mux.Router.DDConfig, added by the add_struct_fields rule in otelc.yaml. One
// is built lazily per Router so a Subrouter, which does not go through
// mux.NewRouter, gets its own on its first request.
type routerConfig struct {
	once          sync.Once
	headerTags    instrumentation.HeaderTags
	serviceName   string
	resourceNamer func(*mux.Router, *http.Request) string
	spanOpts      []tracer.StartSpanOption
}

func (c *routerConfig) init() {
	c.once.Do(func() {
		analyticsRate := instr.AnalyticsRate(true)
		c.headerTags = instr.HTTPHeadersAsTags()
		c.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
		c.resourceNamer = defaultResourceNamer
		c.spanOpts = []tracer.StartSpanOption{
			tracer.Tag(ext.Component, instrumentation.PackageGorillaMux),
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		}
		if !math.IsNaN(analyticsRate) {
			c.spanOpts = append(c.spanOpts, tracer.Tag(ext.EventSampleRate, analyticsRate))
		}
	})
}

// defaultResourceNamer mirrors ddDefaultResourceNamer in orchestrion.yml.
func defaultResourceNamer(router *mux.Router, req *http.Request) string {
	var match mux.RouteMatch
	route := "unknown"
	if router.Match(req, &match) && match.Route != nil {
		if tpl, err := match.Route.GetPathTemplate(); err == nil {
			route = tpl
		}
	}
	return req.Method + " " + route
}

// BeforeServeHTTP is the otelc port of the ServeHTTP prepend-statements
// advice in orchestrion.yml. It hooks the target library's *mux.Router
// directly rather than this contrib's Router wrapper, so mux.NewRouter() and
// every Subrouter it produces are traced without the caller wrapping
// anything - the same reach the orchestrion aspect has.
//
// DDIgnoreRequest is a re-entrancy guard, not a user-facing toggle. Since
// ServeHTTP is hooked at its definition, httptrace.TraceAndServe calling back
// into the (shallow-copied) router would otherwise recurse into this hook
// forever. The orchestrion aspect solves the same problem with an
// always-false ignoreRequest closure that a copy overrides to always-true.
func BeforeServeHTTP(ictx hook.HookContext, r *mux.Router, w http.ResponseWriter, req *http.Request) {
	if r.DDIgnoreRequest {
		return
	}

	cfg, _ := r.DDConfig.(*routerConfig)
	if cfg == nil {
		cfg = &routerConfig{}
		r.DDConfig = cfg
	}
	cfg.init()

	var (
		match    mux.RouteMatch
		route    string
		spanOpts = options.Copy(cfg.spanOpts)
	)
	if r.Match(req, &match) && match.Route != nil {
		if h, err := match.Route.GetHostTemplate(); err == nil {
			spanOpts = append(spanOpts, tracer.Tag("mux.host", h))
		}
		route, _ = match.Route.GetPathTemplate()
	}
	spanOpts = append(spanOpts, instrhttptrace.HeaderTagsFromRequest(req, cfg.headerTags))
	resource := cfg.resourceNamer(r, req)

	rCopy := *r
	rCopy.DDIgnoreRequest = true

	httptrace.TraceAndServe(&rCopy, w, req, &httptrace.ServeConfig{
		Service:     cfg.serviceName,
		Resource:    resource,
		SpanOpts:    spanOpts,
		RouteParams: match.Vars,
		Route:       route,
	})
	ictx.SetSkipCall(true)
}
