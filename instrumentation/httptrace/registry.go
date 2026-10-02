// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net/http"
	"reflect"
	"sync"
)

var (
	routingHandlerTypesMu sync.RWMutex
	routingHandlerTypes   = make(map[reflect.Type]struct{})
)

// RegisterRoutingHandlerType records that values of type T are HTTP handlers
// that already start their own server span (typically a traced router). It is
// meant to be called from a router integration's init function.
//
// When DD_TRACE_HTTP_ROUTER_ROOT_SPAN is enabled, the net/http server
// instrumentation (both Orchestrion and manual WrapHandler) uses this registry
// to avoid wrapping such handlers a second time, which would otherwise produce
// a redundant server span above the router's own span. See
// https://github.com/DataDog/dd-trace-go/issues/3369.
//
// Matching is by exact dynamic type: register the concrete type that is
// assigned to http.Server.Handler (e.g. *chi.Mux). A router wrapped by other
// middleware before reaching the server (h2c, http.TimeoutHandler, ...) will
// not match, and the redundant span is kept.
func RegisterRoutingHandlerType[T http.Handler]() {
	t := reflect.TypeFor[T]()
	routingHandlerTypesMu.Lock()
	routingHandlerTypes[t] = struct{}{}
	routingHandlerTypesMu.Unlock()
}

// IsRoutingHandler reports whether h's dynamic type was registered via
// RegisterRoutingHandlerType. A typed-nil handler (e.g. (*chi.Mux)(nil)) still
// matches on its type; only an untyped-nil handler returns false.
func IsRoutingHandler(h http.Handler) bool {
	if h == nil {
		return false
	}
	t := reflect.TypeOf(h)
	routingHandlerTypesMu.RLock()
	_, ok := routingHandlerTypes[t]
	routingHandlerTypesMu.RUnlock()
	return ok
}
