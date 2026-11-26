// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"
	pathpkg "path"
	"strings"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/pattern"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// ServeMux is an HTTP request multiplexer that traces all the incoming requests.
type ServeMux struct {
	*http.ServeMux
	cfg *internal.Config
}

// NewServeMux allocates and returns an http.ServeMux augmented with the
// global tracer.
func NewServeMux(opts ...internal.Option) *ServeMux {
	instr := internal.Instrumentation
	cfg := internal.Default(instr)
	cfg.ApplyOpts(opts...)
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.Component, internal.ComponentName))
	instr.Logger().Debug("contrib/net/http: Configuring ServeMux: %#v", cfg)

	// wrap the provided ServeMux, if any, otherwise create a new one
	mux := cfg.Mux
	if mux == nil {
		mux = http.NewServeMux()
	} else if internal.Instrumentation.AppSecEnabled() {
		// Routes registered directly on a caller-provided *http.ServeMux (e.g. before it
		// was passed to WithServeMux) never go through this package's Handle/HandleFunc,
		// so they never call httpsec.RouteMatched: the WAF never sees the resolved path
		// parameters for those routes, and can't block on rules that key off of them.
		instr.Logger().Warn("contrib/net/http: WithServeMux was used while AppSec is enabled; " +
			"path-parameter WAF rules only apply to routes registered via ServeMux.Handle or " +
			"ServeMux.HandleFunc, not to routes already registered on the wrapped *http.ServeMux")
	}

	return &ServeMux{
		ServeMux: mux,
		cfg:      cfg,
	}
}

// Handle registers the handler for the given pattern.
func (mux *ServeMux) Handle(pttrn string, inner http.Handler) {
	handlerFunc := inner
	if internal.Instrumentation.AppSecEnabled() {
		// Calling TraceAndServe before `http.ServeMux.ServeHTTP` does not give enough information
		// about routing for AppSec to work properly when using the ServeMux tracing wrapper.
		// Therefore, we need to wrap the handlerFunc with a handler that finished the job here
		// after pattern data and matches are available
		// This also means stopping the handle from being called if security rules disallow it
		handlerFunc = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if httpsec.RouteMatched(r.Context(), pattern.Route(r.Pattern), pattern.PathParameters(r.Pattern, r)) != nil {
				return
			}
			inner.ServeHTTP(w, r)
		})
	}

	mux.ServeMux.Handle(pttrn, handlerFunc)
}

// HandleFunc registers the handler function for the given pattern.
func (mux *ServeMux) HandleFunc(pttrn string, handlerFunc func(http.ResponseWriter, *http.Request)) {
	mux.Handle(pttrn, http.HandlerFunc(handlerFunc))
}

// reportingServeMux serves a request with a ServeMux, then reports its path
// values. A route registered on a caller-provided *http.ServeMux reports no path
// parameters (see NewServeMux). ServeMux sets the pattern and the path values of
// the request that it serves: they go to API Security, which does not send them
// to the WAF. A pattern without wildcards has no path values. A redirect that
// ServeMux generates instead of serving a route reports no route: its pattern can
// be the request path.
type reportingServeMux struct {
	*http.ServeMux
	redirect bool
}

func (mux reportingServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Deferred, so that a handler that panics still reports its path values.
	defer func() {
		switch {
		case mux.redirect:
			httpsec.ReportMatchedRoute(r.Context(), httpsec.MatchedRoute{})
		case strings.Contains(r.Pattern, "{"):
			httpsec.ReportMatchedRoute(r.Context(), httpsec.MatchedRoute{
				Route:  pattern.Route(r.Pattern),
				Params: pattern.PathParameters(r.Pattern, r),
			})
		}
	}()
	mux.ServeMux.ServeHTTP(w, r)
}

// isGeneratedRedirect reports that ServeMux redirects the request instead of
// serving a route, as net/http decides it: its escaped path is not clean (but for
// CONNECT), or it lacks the trailing "/" of the subtree pattern (ending with "/",
// {$} or {name...}) that matches it with a "/" added. pttrn is the pattern that ServeMux.Handler returns for the
// request: for a redirect, the pattern or the path that the redirect leads to.
func isGeneratedRedirect(r *http.Request, pttrn string) bool {
	escaped := r.URL.EscapedPath()
	if r.Method != http.MethodConnect && cleanPath(escaped) != escaped {
		return true
	}
	target := strings.TrimSuffix(pattern.Path(pttrn), "{$}")
	if strings.HasSuffix(target, "...}") {
		target = target[:strings.LastIndexByte(target, '/')+1]
	}
	return !strings.HasSuffix(escaped, "/") && strings.HasSuffix(target, "/") &&
		strings.Count(target, "/") == strings.Count(escaped, "/")+1
}

// cleanPath returns the path that ServeMux redirects a request to: path.Clean,
// which keeps a trailing "/".
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	cleaned := pathpkg.Clean(p)
	if p[len(p)-1] == '/' && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mux.cfg.IgnoreRequest(r) {
		mux.ServeMux.ServeHTTP(w, r)
		return
	}
	// get the resource associated to this request
	_, pttrn := mux.Handler(r)
	var route string
	if mux.cfg.OTelSemanticsEnabled {
		route = pattern.Path(pttrn)
	} else {
		route = pattern.Route(pttrn)
	}
	resource := mux.cfg.ResourceNamer(r)
	if resource == "" && !mux.cfg.OTelSemanticsEnabled {
		resource = r.Method + " " + route
	}
	so := make([]tracer.StartSpanOption, len(mux.cfg.SpanOpts), len(mux.cfg.SpanOpts)+1)
	copy(so, mux.cfg.SpanOpts)
	so = append(so, httptrace.HeaderTagsFromRequest(r, mux.cfg.HeaderTags))
	var h http.Handler = mux.ServeMux
	if internal.Instrumentation.AppSecEnabled() {
		h = reportingServeMux{ServeMux: mux.ServeMux, redirect: isGeneratedRedirect(r, pttrn)}
	}
	TraceAndServe(h, w, r, &httptrace.ServeConfig{
		Service:              mux.cfg.ServiceName,
		ServiceSource:        mux.cfg.ServiceSource,
		Framework:            "net/http",
		Resource:             resource,
		SpanOpts:             so,
		Route:                route,
		OTelSemanticsEnabled: &mux.cfg.OTelSemanticsEnabled,
		IsStatusError:        mux.cfg.IsStatusError,
	})
}
