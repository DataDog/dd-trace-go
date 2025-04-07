// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package negroni provides helper functions for tracing the urfave/negroni package (https://github.com/urfave/negroni).
package negroni

import (
	"fmt"
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"github.com/urfave/negroni"
)

const component = instrumentation.PackageUrfaveNegroni

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageUrfaveNegroni)
}

// DatadogMiddleware returns middleware that will trace incoming requests.
type DatadogMiddleware struct {
	cfg *config
}

func (m *DatadogMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	opts := options.Expand(m.cfg.spanOpts, 0, 4) // opts must be a copy of m.cfg.spanOpts, locally scoped, to avoid races.
	opts = append(opts,
		tracer.ServiceName(m.cfg.serviceName),
		tracer.ResourceName(m.cfg.resourceNamer(r)),
		httptrace.HeaderTagsFromRequest(r, m.cfg.headerTags))
	if !math.IsNaN(m.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, m.cfg.analyticsRate))
	}
	_, ctx, finishSpans := httptrace.StartRequestSpan(r, opts...)
	defer func() {
		// check if the responseWriter is of type negroni.ResponseWriter
		var (
			status int
			opts   []tracer.FinishOption
		)
		responseWriter, ok := w.(negroni.ResponseWriter)
		if ok {
			status = responseWriter.Status()
			if m.cfg.isStatusError(status) {
				opts = []tracer.FinishOption{tracer.WithError(fmt.Errorf("%d: %s", status, http.StatusText(status)))}
			}
		}
		finishSpans(status, m.cfg.isStatusError, opts...)
	}()

	next(w, r.WithContext(ctx))
}

// Middleware create the negroni middleware that will trace incoming requests
func Middleware(opts ...Option) *DatadogMiddleware {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, component))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	instr.Logger().Debug("contrib/urgave/negroni: Configuring Middleware: %#v", cfg)

	m := DatadogMiddleware{
		cfg: cfg,
	}

	return &m
}
