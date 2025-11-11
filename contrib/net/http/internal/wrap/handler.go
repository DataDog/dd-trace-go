// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/pattern"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

type WrappedHandler struct {
	http.HandlerFunc
}

// Handler wraps an [http.Handler] with tracing using the given service and resource.
// If the WithResourceNamer option is provided as part of opts, it will take precedence over the resource argument.
func Handler(h http.Handler, service, resource string, opts ...internal.Option) http.Handler {
	instr := internal.Instrumentation
	cfg := internal.Default(instr)
	cfg.ApplyOpts(opts...)
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.Component, internal.ComponentName))
	instr.Logger().Debug("contrib/net/http: Wrapping Handler: Service: %s, Resource: %s, %#v", service, resource, cfg)
	// if the service provided from parameters is empty,
	// use the one from the config (which should default to DD_SERVICE / "http.router")
	if service == "" {
		service = cfg.ServiceName
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if cfg.IgnoreRequest(req) {
			h.ServeHTTP(w, req)
			return
		}
		resc := resource
		if r := cfg.ResourceNamer(req); r != "" {
			resc = r
		}
		so := make([]tracer.StartSpanOption, len(cfg.SpanOpts), len(cfg.SpanOpts)+1)
		copy(so, cfg.SpanOpts)
		so = append(so, httptrace.HeaderTagsFromRequest(req, cfg.HeaderTags))
		TraceAndServe(h, w, req, &httptrace.ServeConfig{
			Framework:     "net/http",
			Service:       service,
			Resource:      resc,
			FinishOpts:    cfg.FinishOpts,
			SpanOpts:      so,
			IsStatusError: cfg.IsStatusError,
			Route:         pattern.Route(req.Pattern),
			RouteParams:   pattern.PathParameters(req.Pattern, req),
		})
	})
}
