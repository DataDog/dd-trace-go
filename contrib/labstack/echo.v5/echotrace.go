// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package echo provides functions to trace the labstack/echo package (https://github.com/labstack/echo).
package echo

import (
	"fmt"
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"github.com/labstack/echo/v5"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageLabstackEchoV5)
}

// Wrap configures the provided [echo.Echo] and returns it. It:
//   - registers [Middleware] via [echo.Echo.Use],
//   - sets [echo.Echo.OnAddRoute] to [OnAddRoute] for API Catalog reporting,
//   - lazily wraps [echo.Echo.Binder] on the first request, preserving any
//     user-configured Binder as the inner delegate (see notes below).
//
// Wrap must be called before starting the server (e.g. [echo.Echo.Start]) and
// before any concurrent access to the [echo.Echo] instance. Echo itself
// forbids mutating Echo fields after the server has started.
//
// AppSec body monitoring — differences with the echo.v4 integration:
//
// In echo v4, AppSec wrapped the [echo.Context] interface at request time, so
// it was independent of [echo.Echo.Binder] reassignment. [echo.Context] in
// echo v5 is a concrete struct that cannot be wrapped, so AppSec must
// intercept body parsing via [echo.Echo.Binder] instead. To preserve
// flexibility, Wrap installs the AppSec Binder wrap lazily on the first
// request: any custom Binder assigned to [echo.Echo.Binder] between Wrap and
// the first request is captured as the inner delegate, and both run on every
// Bind call (your Binder first, then AppSec monitoring).
//
// Caveat: reassigning [echo.Echo.Binder] after the first request has been
// served will silently disable AppSec body monitoring. Echo itself forbids
// mutating Echo instance fields after the server has started; this falls
// under that same restriction.
//
// It is recommended to use Wrap if you want to benefit from future tracer
// features that require additional properties to be configured without having
// to update your code.
func Wrap(e *echo.Echo, opts ...Option) *echo.Echo {
	// Prepend the internal echo-instance option so user-provided opts can't
	// override it (only Wrap should set this).
	opts = append([]Option{withEchoInstance(e)}, opts...)
	e.Use(Middleware(opts...))
	// Chain OnAddRoute with any user-set callback so we don't silently
	// overwrite the user's hook. The user's callback runs first; if it
	// returns an error, route registration aborts and ours is skipped.
	if prev := e.OnAddRoute; prev != nil {
		e.OnAddRoute = func(r echo.Route) error {
			if err := prev(r); err != nil {
				return err
			}
			return OnAddRoute(r)
		}
	} else {
		e.OnAddRoute = OnAddRoute
	}
	// Binder is intentionally NOT wrapped here. The wrap is applied lazily on
	// the first request (see the per-request closure in Middleware) so that
	// any user-configured Binder assigned after Wrap is still preserved.
	return e
}

// Middleware returns echo middleware that traces incoming requests. Prefer
// [Wrap] for typical setups — it also enables route registration tracking and
// AppSec body monitoring. Use Middleware directly when you need scoped tracing
// (e.g. on a sub-group) or when those features should not be enabled globally.
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/labstack/echo.v5: Configuring Middleware: %#v", cfg)
	spanOpts := make([]tracer.StartSpanOption, 0, 3+len(cfg.tags))
	spanOpts = append(spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
	for k, v := range cfg.tags {
		spanOpts = append(spanOpts, tracer.Tag(k, v))
	}
	spanOpts = append(spanOpts,
		tracer.Tag(ext.Component, instrumentation.PackageLabstackEchoV5),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
	)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			// Lazily install the AppSec Binder wrap on the first request. This
			// preserves any user-set Binder assigned after Wrap as the inner
			// delegate. Only Wrap sets cfg.echoInstance; Middleware used
			// stand-alone (e.g. for scoped tracing) leaves it nil.
			//
			// Safety: sync.Once provides a happens-before barrier, and the
			// echo middleware chain ensures no handler calls c.Bind() before
			// our middleware runs for that request — so concurrent first
			// requests synchronize correctly through bindOnce.
			if cfg.echoInstance != nil {
				cfg.bindOnce.Do(func() {
					if _, ok := cfg.echoInstance.Binder.(*appsecBinder); !ok {
						cfg.echoInstance.Binder = &appsecBinder{inner: cfg.echoInstance.Binder}
					}
				})
			}

			// If we have an ignoreRequestFunc, use it to see if we proceed with tracing
			if cfg.ignoreRequestFunc != nil && cfg.ignoreRequestFunc(c) {
				return next(c)
			}

			request := c.Request()
			route := c.Path()
			resource := request.Method + " " + route
			opts := options.Copy(spanOpts) // opts must be a copy of spanOpts, locally scoped, to avoid races.
			if !math.IsNaN(cfg.analyticsRate) {
				opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
			}
			opts = append(opts,
				tracer.ResourceName(resource),
				tracer.Tag(ext.HTTPRoute, route),
				httptrace.HeaderTagsFromRequest(request, cfg.headerTags))

			var finishOpts []tracer.FinishOption
			if cfg.noDebugStack {
				finishOpts = []tracer.FinishOption{tracer.NoDebugStack()}
			}

			span, ctx, finishSpans := httptrace.StartRequestSpan(request, opts...)

			// pass the span through the request context
			c.SetRequest(request.WithContext(ctx))

			if instr.AppSecEnabled() {
				next = withAppSec(next, span, cfg.echoInstance)
			}
			// serve the request to the next middleware
			err := next(c)
			var echoStatus int
			if err != nil && !shouldIgnoreError(cfg, err) {
				// It is impossible to determine what the final status code of a request is in echo.
				// This is the best we can do.
				if echoErr, ok := cfg.translateError(err); ok {
					if cfg.isStatusError(echoErr.Code) {
						finishOpts = append(finishOpts, tracer.WithError(err))
					}
					echoStatus = echoErr.Code

				} else {
					// Any error that is not an *echo.HTTPError will be treated as an error with 500 status code.
					if cfg.isStatusError(500) {
						finishOpts = append(finishOpts, tracer.WithError(err))
					}
					echoStatus = 500
				}
			} else if status := responseStatus(c); status > 0 {
				if cfg.isStatusError(status) {
					if statusErr := errorFromStatusCode(status); !shouldIgnoreError(cfg, statusErr) {
						finishOpts = append(finishOpts, tracer.WithError(statusErr))
					}
				}
				echoStatus = status
			} else {
				if cfg.isStatusError(200) {
					if statusErr := errorFromStatusCode(200); !shouldIgnoreError(cfg, statusErr) {
						finishOpts = append(finishOpts, tracer.WithError(statusErr))
					}
				}
				echoStatus = 200
			}
			defer func() {
				finishSpans(echoStatus, func(status int) bool {
					if cfg.isStatusError(status) {
						if statusErr := errorFromStatusCode(status); !shouldIgnoreError(cfg, statusErr) {
							return true
						}
					}
					return false
				}, finishOpts...)
			}()
			return err
		}
	}
}

// responseStatus extracts the HTTP status code from the echo response.
// In v5, c.Response() returns http.ResponseWriter; we unwrap it to access the
// underlying *echo.Response which holds the Status field.
func responseStatus(c *echo.Context) int {
	if r, err := echo.UnwrapResponse(c.Response()); err == nil {
		return r.Status
	}
	return 0
}

func errorFromStatusCode(statusCode int) error {
	return fmt.Errorf("%d: %s", statusCode, http.StatusText(statusCode))
}

func shouldIgnoreError(cfg *config, err error) bool {
	return cfg.errCheck != nil && !cfg.errCheck(err)
}
