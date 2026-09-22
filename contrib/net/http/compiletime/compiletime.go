// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package compiletime holds the entry points that compile-time
// auto-instrumentation calls into.
//
// Orchestrion reaches ../internal/orchestrion through go:linkname, so that
// package can stay internal. An otelc hook lives in its own module and cannot,
// hence this package.
package compiletime

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/orchestrion"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
)

// AfterRoundTrip finishes the span ObserveRoundTrip started.
type AfterRoundTrip = wrap.AfterRoundTrip

// ObserveRoundTrip starts a client span for req and returns the request to send
// in its place, carrying the propagation headers.
func ObserveRoundTrip(req *http.Request) (*http.Request, AfterRoundTrip, error) {
	return orchestrion.ObserveRoundTrip(req)
}

// WrapHandler returns handler wrapped in the server-side tracing handler.
func WrapHandler(handler http.Handler) http.Handler {
	return orchestrion.WrapHandler(handler)
}
