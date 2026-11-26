// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gin provides functions to trace the gin-gonic/gin package (https://github.com/gin-gonic/gin).
package gin // import "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

const componentName = "gin-gonic/gin"

var instr *instrumentation.Instrumentation

// errHandlerPanic is the span error when a handler panics. It does not contain
// the panic value, because that value can contain request data or credentials.
var errHandlerPanic = errors.New("handler panicked")

func init() {
	instr = instrumentation.Load(instrumentation.PackageGin)
}

// Middleware returns middleware that will trace incoming requests. If service is empty then the
// default service name will be used.
func Middleware(service string, opts ...Option) gin.HandlerFunc {
	cfg := newConfig(service)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/gin-gonic/gin: Configuring Middleware: Service: %s, %#v", cfg.serviceName, cfg)
	spanOpts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
	}
	return func(c *gin.Context) {
		if cfg.ignoreRequest(c) {
			return
		}
		route := c.FullPath()
		opts := options.Expand(spanOpts, 0, 4) // opts must be a copy of spanOpts, locally scoped, to avoid races.
		opts = append(opts, tracer.ResourceName(cfg.resourceName(c, route)))
		if !math.IsNaN(cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		opts = append(opts, cfg.routeTag(route, c.Request))
		opts = append(opts, httptrace.HeaderTagsFromRequest(c.Request, cfg.headerTags))
		span, ctx, finishSpans := httptrace.StartRequestSpan(c.Request, opts...)
		var (
			normal    bool
			panicked  bool
			recovered any
		)
		defer func() {
			// Reached on normal return, after a panic was recovered below, or
			// while runtime.Goexit unwinds (panicked stays false there).
			finishSpan(cfg, c, finishSpans, panicked, recovered)
			if panicked {
				// A recovery middleware earlier in the chain only runs once the
				// span is finished, so the panic is re-raised after recording it.
				panic(recovered)
			}
		}()

		// pass the span through the request context
		c.Request = c.Request.WithContext(ctx)

		// Use AppSec if enabled by user
		if instr.AppSecEnabled() {
			useAppSec(c, httptrace.AppSecSpanTagSetter(span, cfg.otelEnabled))
		}

		// serve the request to the next middleware
		func() {
			defer func() {
				if !normal {
					// recover returns nil for runtime.Goexit and, with
					// GODEBUG=panicnil=1, for panic(nil). Goexit keeps unwinding
					// past this function, so the code below only runs for panics.
					recovered = recover()
				}
			}()
			c.Next()
			normal = true
		}()
		panicked = !normal

		if len(c.Errors) > 0 {
			span.SetTag("gin.errors", c.Errors.String())
		}
	}
}

func (cfg *config) resourceName(c *gin.Context, route string) string {
	if cfg.resourceNamer != nil {
		return cfg.resourceNamer(c)
	}
	if cfg.otelEnabled {
		return httptrace.ServerSpanName(c.Request.Method, route)
	}
	return defaultResourceNamer(c)
}

func (cfg *config) routeTag(route string, req *http.Request) tracer.StartSpanOption {
	if cfg.otelEnabled {
		return httptrace.HTTPEndpointTag(route, req)
	}
	return tracer.Tag(ext.HTTPRoute, route)
}

func finishSpan(cfg *config, c *gin.Context, finishSpans httptrace.FinishSpanFunc, panicked bool, recovered any) {
	status := c.Writer.Status()
	var finishOpts []tracer.FinishOption
	if panicked && recovered != http.ErrAbortHandler {
		if !c.Writer.Written() && !isBrokenConnection(recovered) {
			// Nothing has been sent yet, so the response is going to be a 500.
			status = http.StatusInternalServerError
		}
		finishOpts = append(finishOpts, tracer.WithError(errHandlerPanic))
	}
	statusError := cfg.isStatusError != nil && cfg.isStatusError(status)
	if cfg.useGinErrors && (statusError || len(finishOpts) > 0) && len(c.Errors) > 0 {
		// Applied after the panic error so that the errors the user opted into win.
		finishOpts = append(finishOpts, tracer.WithError(errors.New(c.Errors.String())))
	}
	if cfg.isStatusError == nil {
		finishSpans(status, nil, finishOpts...)
		return
	}
	finishSpans(status, func(int) bool { return statusError }, finishOpts...)
}

// isBrokenConnection reports whether v is a panic that gin.Recovery treats as a
// dead connection, for which it aborts without writing a status.
func isBrokenConnection(v any) bool {
	ne, ok := v.(*net.OpError)
	if !ok || ne == nil {
		return false
	}
	var se *os.SyscallError
	if !errors.As(ne, &se) || se == nil {
		return false
	}
	msg := strings.ToLower(se.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	span, _ := tracer.StartSpanFromContext(c.Request.Context(), "gin.render.html")
	span.SetTag("go.template", name)
	span.SetTag(ext.Component, componentName)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("error rendering tmpl:%s: %s", name, r)
			span.Finish(tracer.WithError(err))
			panic(r)
		}
		span.Finish()
	}()
	c.HTML(code, name, obj)
}
