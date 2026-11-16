// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// withAppsec returns the handler that runs the AppSec operation of a request.
// The handler expects the chi response writer that [Middleware] creates.
//
// With Use before routing, chi has not selected the route when this middleware
// starts: the WAF gets the route and the path parameters that chi knows at that
// time, as before the route was reported. After the handler, the handler reports
// the route that chi selected (RouteMatched without parameters), because API
// Security uses the route as the sampling key. It gives no path parameters to the
// WAF at any time. The response writer is wrapped in a commitTracker (see
// writer_appsec.go) to know if a late block can still replace the response.
func withAppsec(next http.Handler, span trace.TagSetter, cfg *config, cache *sync.Map) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A copy for each request: httpsec.BeforeHandle writes in the config.
		appsecCfg := cfg.appsecConfig

		tw := w
		if ww, ok := w.(middleware.WrapResponseWriter); ok {
			tw = newCommitTracker(ww)
		}

		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			httpsec.WrapHandler(next, span, &appsecCfg).ServeHTTP(tw, r)
			return
		}

		// What chi knows now (for example with r.With(Middleware())): the input of
		// AppSec before the route was reported.
		route, params := cfg.modifyResourceName(rctx.RoutePattern()), knownParams(rctx)
		appsecCfg.Route, appsecCfg.RouteParams = route, params

		late := http.HandlerFunc(func(w http.ResponseWriter, tr *http.Request) {
			next.ServeHTTP(w, tr)

			rc := chi.RouteContext(tr.Context())
			if rc == nil {
				return
			}
			if finalRoute := reportedRoute(rc, cfg, cache); finalRoute != route {
				// The error is ignored: afterHandle applies the stored action, and the
				// commitTracker tells if the response can still be replaced.
				_ = httpsec.RouteMatched(tr.Context(), finalRoute, nil)
			}
		})

		httpsec.WrapHandler(late, span, &appsecCfg).ServeHTTP(tw, r)
	})
}

// knownParams returns the parameters that chi matched so far, as AppSec read them
// before the route was reported: the first value of a name that is used at several
// levels wins.
func knownParams(rctx *chi.Context) map[string]string {
	keys, values := rctx.URLParams.Keys, rctx.URLParams.Values
	if len(keys) == 0 || len(keys) != len(values) {
		return nil
	}
	params := make(map[string]string, len(keys))
	for i, key := range keys {
		if _, found := params[key]; !found {
			params[key] = values[i]
		}
	}
	return params
}
