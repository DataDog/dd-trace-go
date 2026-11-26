// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package endpointhook

import (
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
)

var woven atomic.Bool

type endpoint struct {
	handler         http.Handler
	includeWildcard bool
}

// Wrap is called at route registration, which can occur before package init.
func Wrap(h http.Handler, includeWildcard bool) http.Handler {
	woven.Store(true)
	if h == nil {
		return nil
	}
	return &endpoint{handler: h, includeWildcard: includeWildcard}
}

type mountAliases struct {
	handler  http.Handler
	endpoint http.Handler
	pattern  string
}

// WrapMountAliases reports terminal mount aliases that chi marks as stubs.
func WrapMountAliases(h http.Handler, pattern string) http.Handler {
	if h == nil || len(pattern) == 0 || pattern[len(pattern)-1] == '/' {
		return h
	}
	return &mountAliases{handler: h, endpoint: Wrap(h, false), pattern: pattern}
}

func (m *mountAliases) Unwrap() http.Handler { return m.handler }

func (m *mountAliases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rctx := chi.RouteContext(r.Context())
	if rctx != nil && len(rctx.RoutePatterns) > 0 {
		pattern := rctx.RoutePatterns[len(rctx.RoutePatterns)-1]
		if pattern == m.pattern || pattern == m.pattern+"/" {
			m.endpoint.ServeHTTP(w, r)
			return
		}
	}
	m.handler.ServeHTTP(w, r)
}

func (e *endpoint) Unwrap() http.Handler { return e.handler }

func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	marker, ok := r.Context().Value(markerKey{}).(*Marker)
	if !ok || marker.Disabled {
		e.handler.ServeHTTP(w, r)
		return
	}
	rctx := chi.RouteContext(r.Context())
	if rctx == nil || len(rctx.RoutePatterns) <= marker.Depth {
		e.handler.ServeHTTP(w, r)
		return
	}
	var params map[string]string
	for i, key := range rctx.URLParams.Keys {
		if key == "*" && !e.includeWildcard {
			continue
		}
		if params == nil {
			params = make(map[string]string, len(rctx.URLParams.Keys))
		}
		params[key] = rctx.URLParams.Values[i]
	}
	if len(params) == 0 {
		e.handler.ServeHTTP(w, r)
		return
	}
	if err := routeMatched(r.Context(), marker.Route.ReportedRoute(rctx), params); err != nil {
		if op, ok := dyngo.FindOperation[httpsec.HandlerOperation](r.Context()); ok {
			dyngo.EmitData(op, httpsec.EarlyBlock{})
			if !op.ContextOperation.BlockingUnavailable() {
				return
			}
		} else {
			return
		}
	}
	e.handler.ServeHTTP(w, r)
}
