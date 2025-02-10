// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http/internal/config"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type WrappedHandler struct {
	http.HandlerFunc
}

// Handler wraps an [http.Handler] with tracing using the given service and resource.
// If the WithResourceNamer option is provided as part of opts, it will take precedence over the resource argument.
func Handler(h http.Handler, service, resource string, opts ...config.Option) http.Handler {
	cfg := config.With(opts...)

	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.Component, config.ComponentName))
	log.Debug("contrib/net/http: Wrapping Handler: Service: %s, Resource: %s, %#v", service, resource, cfg)
	return WrappedHandler{
		func(w http.ResponseWriter, req *http.Request) {
			if cfg.IgnoreRequest(req) {
				h.ServeHTTP(w, req)
				return
			}
			resc := resource
			if r := cfg.ResourceNamer(req); r != "" {
				resc = r
			}
			so := make([]ddtrace.StartSpanOption, len(cfg.SpanOpts), len(cfg.SpanOpts)+1)
			copy(so, cfg.SpanOpts)
			so = append(so, httptrace.HeaderTagsFromRequest(req, cfg.HeaderTags))
			pattern := getPattern(nil, req)
			TraceAndServe(h, w, req, &httptrace.ServeConfig{
				Service:       service,
				Resource:      resc,
				FinishOpts:    cfg.FinishOpts,
				SpanOpts:      so,
				IsStatusError: cfg.IsStatusError,
				Route:         patternRoute(pattern),
				RouteParams:   patternValues(pattern, req),
			})
		},
	}
}
