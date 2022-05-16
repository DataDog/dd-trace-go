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

	"github.com/urfave/negroni"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// DatadogMiddleware returns middleware that will trace incoming requests.
type DatadogMiddleware struct {
	cfg *config
}

func (m *DatadogMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	opts := m.cfg.spanOpts
	if !math.IsNaN(m.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, m.cfg.analyticsRate))
	}
	span, ctx := httptrace.StartRequestSpan(r, m.cfg.serviceName, m.cfg.resourceNamer(r), false, opts...)
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
		httptrace.FinishRequestSpan(span, status, opts...)
	}()

	next(w, r.WithContext(ctx))
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
