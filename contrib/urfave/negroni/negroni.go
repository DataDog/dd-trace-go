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
	"strconv"

	"github.com/urfave/negroni"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// DatadogMiddleware returns middleware that will trace incoming requests.
type DatadogMiddleware struct {
	cfg *config
}

func (m *DatadogMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeWeb),
		tracer.ServiceName(m.cfg.serviceName),
		tracer.Tag(ext.HTTPMethod, r.Method),
		tracer.Tag(ext.HTTPURL, r.URL.Path),
		tracer.Tag(ext.ResourceName, m.cfg.resourceNamer(r)),
		tracer.Measured(),
	}
	if !math.IsNaN(m.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, m.cfg.analyticsRate))
	}
	if spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
		opts = append(opts, tracer.ChildOf(spanctx))
	}
	opts = append(opts, m.cfg.spanOpts...)
	span, ctx := tracer.StartSpanFromContext(r.Context(), "http.request", opts...)
	defer span.Finish()

	r = r.WithContext(ctx)

	next(w, r)

	// check if the responseWriter is of type negroni.ResponseWriter
	responseWriter, ok := w.(negroni.ResponseWriter)
	if ok {
		status := responseWriter.Status()
		span.SetTag(ext.HTTPCode, strconv.Itoa(status))
		if m.cfg.isStatusError(status) {
			// mark 5xx server error
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", status, http.StatusText(status)))
		}
	}
}

// Middleware create the negroni middleware that will trace incoming requests
func Middleware(opts ...Option) *DatadogMiddleware {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/urgave/negroni: Configuring Middleware: %#v", cfg)

	m := DatadogMiddleware{
		cfg: cfg,
	}

	return &m
}
