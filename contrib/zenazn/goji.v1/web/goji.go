// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package web provides functions to trace the zenazn/goji/web package (https://github.com/zenazn/goji).
package web // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/zenazn/goji.v1/web"

import (
	"fmt"
	"math"
	"net/http"
	"sync"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/zenazn/goji/web"
)

const componentName = "zenazn/goji.v1/web"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGojiV1Web)
}

// Middleware returns a goji middleware function that will trace incoming requests.
// If goji's Router middleware is also installed, the tracer will be able to determine
// the original route name (e.g. "/user/:id"), and include it as part of the traces' resource
// names.
func Middleware(opts ...Option) func(*web.C, http.Handler) http.Handler {
	var (
		cfg      config
		warnonce sync.Once
	)
	defaults(&cfg)
	for _, fn := range opts {
		fn(&cfg)
	}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, componentName))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))

	log.Debug("contrib/zenazn/goji.v1/web: Configuring Middleware: %#v", cfg)
	return func(c *web.C, h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource := r.Method
			p := web.GetMatch(*c).RawPattern()
			route := ""
			if p != nil {
				route = fmt.Sprintf("%s", p)
				resource = resource + " " + route
			} else {
				warnonce.Do(func() {
					log.Warn("contrib/zenazn/goji.v1/web: routes are unavailable. To enable them add the goji Router middleware before the tracer middleware.")
				})
			}
			sc := &httptrace.ServeConfig{
				Service:    cfg.serviceName,
				Resource:   resource,
				FinishOpts: make([]v2.FinishOption, 1),
				SpanOpts:   make([]v2.StartSpanOption, 1),
				Route:      route,
			}
			sc.FinishOpts[0] = tracer.ApplyV1FinishOptions(cfg.finishOpts...)
			sc.SpanOpts[0] = tracer.ApplyV1Options(cfg.spanOpts...)
			httptrace.TraceAndServe(h, w, r, sc)
		})
	}
}
