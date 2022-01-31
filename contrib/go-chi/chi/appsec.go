// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"

	"github.com/go-chi/chi"
)

func withAppsec(next http.Handler, r *http.Request, span tracer.Span) http.Handler {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return httpsec.WrapHandler(next, span, nil)
	}
	var pathParams map[string]string
	keys := rctx.URLParams.Keys
	values := rctx.URLParams.Values
	if len(keys) > 0 && len(keys) == len(values) {
		pathParams = make(map[string]string, len(keys))
		for i, key := range keys {
			pathParams[key] = values[i]
		}
	}
	return httpsec.WrapHandler(next, span, pathParams)
}
