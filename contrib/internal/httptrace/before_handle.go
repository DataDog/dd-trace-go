// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
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
	// isStatusError allows customization of error code determination.
	IsStatusError func(int) bool
}

// BeforeHandle contains functionality that should be executed before a http.Handler runs.
// It returns the "traced" http.ResponseWriter and http.Request, an additional afterHandle function
// that should be executed after the Handler runs, and a handled bool that instructs if the request has been handled
// or not - in case it was handled, the original handler should not run.
func BeforeHandle(cfg *ServeConfig, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func(), bool) {
	if cfg == nil {
		cfg = new(ServeConfig)
	}
	opts := options.Copy(cfg.SpanOpts...) // make a copy of cfg.SpanOpts to avoid races
	if cfg.Service != "" {
		opts = append(opts, tracer.ServiceName(cfg.Service))
	}
	if cfg.Resource != "" {
		opts = append(opts, tracer.ResourceName(cfg.Resource))
	}
	if cfg.Route != "" {
		opts = append(opts, tracer.Tag(ext.HTTPRoute, cfg.Route))
	}
	span, ctx, finishSpans := StartRequestSpan(r, opts...)
	rw, ddrw := wrapResponseWriter(w)
	rt := r.WithContext(ctx)
	closeSpan := func() {
		finishSpans(ddrw.status, cfg.IsStatusError, cfg.FinishOpts...)
	}
	afterHandle := closeSpan
	handled := false
	if appsec.Enabled() {
		secW, secReq, secAfterHandle, secHandled := httpsec.BeforeHandle(rw, rt, span, cfg.RouteParams, nil)
		afterHandle = func() {
			secAfterHandle()
			closeSpan()
		}
		rw = secW
		rt = secReq
		handled = secHandled
	}
	return rw, rt, afterHandle, handled
}
