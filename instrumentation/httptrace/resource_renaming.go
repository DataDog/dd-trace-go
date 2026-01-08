// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// SetHTTPEndpoint applies the http.endpoint tag to the provided span when the
// resource renaming feature is active. The endpoint is derived from the
// route, when available, or from the request URL path using the same
// simplification rules used by net/http instrumentation.
// It returns the endpoint value when set, or an empty string when resource
// renaming is disabled.
func SetHTTPEndpoint(span *tracer.Span, route string, r *http.Request) string {
	if span == nil {
		return ""
	}
	endpoint, ok := computeHTTPEndpoint(route, r)
	if !ok {
		return ""
	}
	span.SetTag(ext.HTTPEndpoint, endpoint)
	return endpoint
}

func computeHTTPEndpoint(route string, r *http.Request) (string, bool) {
	if !resourceRenamingEnabled() || r == nil || r.URL == nil {
		return "", false
	}

	httpURL := r.URL.EscapedPath()
	if cfg.resourceRenamingAlwaysSimplifiedEndpoint {
		return simplifyHTTPUrl(httpURL), true
	}
	if route != "" {
		return route, true
	}
	return simplifyHTTPUrl(httpURL), true
}

func resourceRenamingEnabled() bool {
	if cfg.resourceRenamingEnabled != nil {
		return *cfg.resourceRenamingEnabled
	}
	return cfg.appsecEnabledMode()
}
