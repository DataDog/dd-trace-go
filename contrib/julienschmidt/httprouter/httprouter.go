// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httprouter provides functions to trace the julienschmidt/httprouter package (https://github.com/julienschmidt/httprouter).
package httprouter // import "github.com/DataDog/dd-trace-go/contrib/julienschmidt/httprouter/v2"

import (
	"net/http"
<<<<<<< HEAD
	"strings"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	httptraceinstr "github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
=======
>>>>>>> origin

	"github.com/julienschmidt/httprouter"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter/internal/tracing"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

<<<<<<< HEAD
var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageJulienschmidtHTTPRouter)
}

=======
>>>>>>> origin
// Router is a traced version of httprouter.Router.
type Router struct {
	*httprouter.Router
	config *tracing.Config
}

// New returns a new router augmented with tracing.
func New(opts ...RouterOption) *Router {
<<<<<<< HEAD
	cfg := new(routerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}

	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, instrumentation.PackageJulienschmidtHTTPRouter))

	instr.Logger().Debug("contrib/julienschmidt/httprouter: Configuring Router: %#v", cfg)
=======
	cfg := tracing.NewConfig(opts...)
	log.Debug("contrib/julienschmidt/httprouter: Configuring Router: %#v", cfg)
>>>>>>> origin
	return &Router{httprouter.New(), cfg}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	tw, treq, afterHandle, handled := tracing.BeforeHandle(r.config, r.Router, wrapRouter, w, req)
	defer afterHandle()
	if handled {
		return
	}
<<<<<<< HEAD
	resource := req.Method + " " + route
	spanOpts := options.Expand(r.config.spanOpts, 0, 1) // spanOpts must be a copy of r.config.spanOpts, locally scoped, to avoid races.
	spanOpts = append(spanOpts, httptraceinstr.HeaderTagsFromRequest(req, r.config.headerTags))

	httptrace.TraceAndServe(r.Router, w, req, &httptrace.ServeConfig{
		Service:  r.config.serviceName,
		Resource: resource,
		SpanOpts: spanOpts,
		Route:    route,
	})
=======
	r.Router.ServeHTTP(tw, treq)
}

type wRouter struct {
	*httprouter.Router
}

func wrapRouter(r *httprouter.Router) tracing.Router {
	return &wRouter{r}
}

func (w wRouter) Lookup(method string, path string) (any, []tracing.Param, bool) {
	h, params, ok := w.Router.Lookup(method, path)
	return h, wrapParams(params), ok
}

type wParam struct {
	httprouter.Param
}

func wrapParams(params httprouter.Params) []tracing.Param {
	wParams := make([]tracing.Param, len(params))
	for i, p := range params {
		wParams[i] = wParam{p}
	}
	return wParams
}

func (w wParam) GetKey() string {
	return w.Key
}

func (w wParam) GetValue() string {
	return w.Value
>>>>>>> origin
}
