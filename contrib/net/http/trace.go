// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

<<<<<<< HEAD
//go:generate sh -c "go run ./internal/make_responsewriter | gofmt > trace_gen.go"

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
=======
import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
>>>>>>> origin
)

const componentName = instrumentation.PackageNetHTTP

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageNetHTTP)
}

// ServeConfig specifies the tracing configuration when using TraceAndServe.
<<<<<<< HEAD
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
	FinishOpts []tracer.FinishOption
	// SpanOpts specifies any options to be applied to the request starting span.
	SpanOpts []tracer.StartSpanOption
}
=======
type ServeConfig = httptrace.ServeConfig
>>>>>>> origin

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
<<<<<<< HEAD
	if cfg == nil {
		cfg = new(ServeConfig)
	}
	opts := options.Expand(cfg.SpanOpts, 2, 3) // make a copy of cfg.SpanOpts to avoid races.
	// Pre-append span.kind and component tags to the options so that they can be overridden.
	opts[0] = tracer.Tag(ext.SpanKind, ext.SpanKindServer)
	opts[1] = tracer.Tag(ext.Component, componentName)
	if cfg.Service != "" {
		opts = append(opts, tracer.ServiceName(cfg.Service))
	}
	if cfg.Resource != "" {
		opts = append(opts, tracer.ResourceName(cfg.Resource))
	}
	if cfg.Route != "" {
		opts = append(opts, tracer.Tag(ext.HTTPRoute, cfg.Route))
	}
	span, ctx := httptrace.StartRequestSpan(r, opts...)
	rw, ddrw := wrapResponseWriter(w)
	defer func() {
		httptrace.FinishRequestSpan(span, ddrw.status, cfg.FinishOpts...)
	}()
	if instr.AppSecEnabled() {
		h = httpsec.WrapHandler(h, span, cfg.RouteParams, nil)
	}
	h.ServeHTTP(rw, r.WithContext(ctx))
}

// responseWriter is a small wrapper around an http response writer that will
// intercept and store the status of a request.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, 0}
}

// Status returns the status code that was monitored.
func (w *responseWriter) Status() int {
	return w.status
}

// Write writes the data to the connection as part of an HTTP reply.
// We explicitly call WriteHeader with the 200 status code
// in order to get it reported into the span.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// WriteHeader sends an HTTP response header with status code.
// It also sets the status code to the span.
func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
=======
	tw, tr, afterHandle, handled := httptrace.BeforeHandle(cfg, w, r)
	defer afterHandle()

	if handled {
>>>>>>> origin
		return
	}
	h.ServeHTTP(tw, tr)
}
