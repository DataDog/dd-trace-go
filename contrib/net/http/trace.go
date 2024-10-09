// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

//go:generate sh -c "go run make_responsewriter.go | gofmt > trace_gen.go"

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http/internal/tracing"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "net/http"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig = tracing.ServeConfig

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	tw, tr, afterHandle := tracing.BeforeHandle(cfg, w, r)
	defer afterHandle()

	h.ServeHTTP(tw, tr)
}
