// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mux provides tracing functions for tracing the gorilla/mux package (https://github.com/gorilla/mux).
package mux // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"

import (
	"net/http"
	"sync/atomic"

	v2 "github.com/DataDog/dd-trace-go/contrib/gorilla/mux/v2"

	"github.com/gorilla/mux"
)

// Router registers routes to be matched and dispatches a handler.
type Router struct {
	*mux.Router
	wrappedRouter *v2.Router

	// Due to the way we wrap v2 API, we need to keep reference to the original options
	// to be able to wrap the router on the fly with the same options.
	realRouter *v2.Router
	resolved   atomic.Uint32
	opts       []RouterOption
}

// StrictSlash defines the trailing slash behavior for new routes. The initial
// value is false.
//
// When true, if the route path is "/path/", accessing "/path" will perform a redirect
// to the former and vice versa. In other words, your application will always
// see the path as specified in the route.
//
// When false, if the route path is "/path", accessing "/path/" will not match
// this route and vice versa.
//
// The re-direct is a HTTP 301 (Moved Permanently). Note that when this is set for
// routes with a non-idempotent method (e.g. POST, PUT), the subsequent re-directed
// request will be made as a GET by most clients. Use middleware or client settings
// to modify this behaviour as needed.
//
// Special case: when a route sets a path prefix using the PathPrefix() method,
// strict slash is ignored for that route because the redirect behavior can't
// be determined from a prefix alone. However, any subrouters created from that
// route inherit the original StrictSlash setting.
func (r *Router) StrictSlash(value bool) *Router {
	r.Router.StrictSlash(value)
	return r
}

// SkipClean defines the path cleaning behaviour for new routes. The initial
// value is false. Users should be careful about which routes are not cleaned
//
// When true, if the route path is "/path//to", it will remain with the double
// slash. This is helpful if you have a route like: /fetch/http://xkcd.com/534/
//
// When false, the path will be cleaned, so /fetch/http://xkcd.com/534/ will
// become /fetch/http/xkcd.com/534
func (r *Router) SkipClean(value bool) *Router {
	r.Router.SkipClean(value)
	return r
}

// UseEncodedPath tells the router to match the encoded original path
// to the routes.
// For eg. "/path/foo%2Fbar/to" will match the path "/path/{var}/to".
//
// If not called, the router will match the unencoded path to the routes.
// For eg. "/path/foo%2Fbar/to" will match the path "/path/foo/bar/to"
func (r *Router) UseEncodedPath() *Router {
	r.Router.UseEncodedPath()
	return r
}

// NewRouter returns a new router instance traced with the global tracer.
func NewRouter(opts ...RouterOption) *Router {
	r := v2.NewRouter(opts...)
	return &Router{
		Router: r.Router,
		opts:   opts,
	}
}

// ServeHTTP dispatches the request to the handler
// whose pattern most closely matches the request URL.
// We only need to rewrite this function to be able to trace
// all the incoming requests to the underlying multiplexer
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.resolved.CompareAndSwap(0, 1) {
		if r.wrappedRouter == nil {
			// If this field is nil, it means that the router has not been created
			// with WrapRouter. We wrap the assigned router on the fly with no options.
			r.wrappedRouter = v2.WrapRouter(r.Router, r.opts...)
		}
		// We consolidate the router that should be used to serve the request.
		// This is done to allow for assigning a sub-router to the main router.
		if r.realRouter == nil {
			r.realRouter = r.wrappedRouter
		}
	}
	r.realRouter.ServeHTTP(w, req)
}

// WrapRouter returns the given router wrapped with the tracing of the HTTP
// requests and responses served by the router.
func WrapRouter(router *mux.Router, opts ...RouterOption) *Router {
	r := v2.WrapRouter(router, opts...)
	return &Router{
		Router:        router,
		wrappedRouter: r,
		opts:          opts,
	}
}
