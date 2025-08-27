// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig struct {
	// Framework is the name of the framework or library being used (optional).
	Framework string
	// Service specifies the service name to use. If left blank, the global service name
	// will be inherited.
	Service string
	// Resource optionally specifies the resource name for this request.
	Resource string
	// QueryParams should be true in order to append the URL query values to the  "http.url" tag.
	QueryParams bool
	// Route is the request matched route if any, if empty, a quantization algorithm will create one using the request URL.
	Route string
	// RouteParams specifies framework-specific route parameters (e.g. for route /user/:id coming
	// in as /user/123 we'll have {"id": "123"}). This field is optional and is used for monitoring
	// by AppSec. It is only taken into account when AppSec is enabled.
	RouteParams map[string]string
	// FinishOpts specifies any options to be used when finishing the request span.
	FinishOpts []tracer.FinishOption
	// SpanOpts specifies any options to be applied to the request starting span.
	SpanOpts []tracer.StartSpanOption
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
	opts := options.Expand(cfg.SpanOpts, 2, 3)
	// Pre-append span.kind, component and http.route tags to the options so that they can be overridden.
	opts[0] = tracer.Tag(ext.SpanKind, ext.SpanKindServer)
	opts[1] = tracer.Tag(ext.Component, "net/http")
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
		route := cfg.Route
		if route == "" {
			route = QuantizeURL(r.URL.EscapedPath())
		}
		appsecConfig := &httpsec.Config{
			Framework:   cfg.Framework,
			Route:       route,
			RouteParams: cfg.RouteParams,
		}

		secW, secReq, secAfterHandle, secHandled := httpsec.BeforeHandle(rw, rt, span, appsecConfig)
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
