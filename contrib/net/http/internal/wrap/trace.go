// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
)

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *httptrace.ServeConfig) {
	tw, tr, afterHandle, handled := httptrace.BeforeHandle(cfg, w, r)
	defer afterHandle()

	if handled {
		return
	}
	h.ServeHTTP(tw, tr)
}
