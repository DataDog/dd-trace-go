// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "net/http"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig = httptrace.ServeConfig

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	tw, tr, afterHandle, handled := httptrace.BeforeHandle(cfg, w, r)
	defer afterHandle()

	if handled {
		return
	}
	h.ServeHTTP(tw, tr)
}
