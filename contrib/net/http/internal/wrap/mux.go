// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"
	"net/url"
	pathpkg "path"
	"reflect"
	"sync"

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

// matchedRoute returns the route that ServeMux selects for r, for API Security,
// given the handler and the pattern that ServeMux.Handler returns for r: it is
// known before AppSec can block the request, and a handler that changes its path
// values (SetPathValue) does not change it. Its path is the path that ServeMux
// matches: the escaped path, cleaned but for CONNECT.
//
// A redirect that ServeMux generates gets the registered pattern that it leads to,
// but for CONNECT: the pattern of its trailing-slash redirect is the unescaped
// redirect path, which is request data. A request that ServeMux answers without a
// pattern ("*") gets no route, as all requests with GODEBUG=httpmuxgo121=1, whose
// patterns are literal paths.
func matchedRoute(r *http.Request, h http.Handler, pttrn string) httpsec.MatchedRoute {
	if r.RequestURI == "*" || legacyServeMux() || isConnectRedirect(r, h, pttrn) {
		return httpsec.MatchedRoute{}
	}
	path := r.URL.EscapedPath()
	if r.Method != http.MethodConnect {
		path = cleanPath(path)
	}
	return httpsec.MatchedRoute{Route: pattern.Route(pttrn), Path: path}
}

// isConnectRedirect reports that ServeMux redirects a CONNECT request to its
// path with a "/" added: its handler is a redirect to its pattern, on the same
// host, which is that unescaped path (http.Redirect cleans the location). A
// redirect handler registered for a pattern redirects elsewhere and keeps its
// route.
func isConnectRedirect(r *http.Request, h http.Handler, pttrn string) bool {
	if r.Method != http.MethodConnect || reflect.TypeOf(h) != redirectHandlerType {
		return false
	}
	var w headerRecorder
	h.ServeHTTP(&w, r)
	location, err := url.Parse(w.Header().Get("Location"))
	return err == nil && location.Host == "" && location.Path == cleanPath(pttrn)
}

// headerRecorder records the headers that a redirect handler writes.
type headerRecorder struct{ header http.Header }

func (w *headerRecorder) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header, 2)
	}
	return w.header
}

func (*headerRecorder) Write(b []byte) (int, error) { return len(b), nil }

func (*headerRecorder) WriteHeader(int) {}

// redirectHandlerType is the type of the handler of a redirect that ServeMux
// generates.
var redirectHandlerType = reflect.TypeOf(http.RedirectHandler("/", http.StatusTemporaryRedirect))

// legacyServeMux reports that ServeMux uses the matcher of GODEBUG=httpmuxgo121=1,
// which net/http selects when it starts: a probe ServeMux does not match a
// wildcard.
var legacyServeMux = sync.OnceValue(func() bool {
	mux := http.NewServeMux()
	mux.HandleFunc("/{a}", func(http.ResponseWriter, *http.Request) {})
	_, pttrn := mux.Handler(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/x"}})
	return pttrn != "/{a}"
})

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
	h, pttrn := mux.Handler(r)
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
	matched := matchedRoute(r, h, pttrn)
	traceAndServe(mux.ServeMux, w, r, &httptrace.ServeConfig{
		Service:              mux.cfg.ServiceName,
		ServiceSource:        mux.cfg.ServiceSource,
		Framework:            "net/http",
		Resource:             resource,
		SpanOpts:             so,
		Route:                route,
		OTelSemanticsEnabled: &mux.cfg.OTelSemanticsEnabled,
		IsStatusError:        mux.cfg.IsStatusError,
	}, &matched)
}
