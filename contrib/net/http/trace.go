// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig = httptrace.ServeConfig

// TraceAndServe serves h with tracing configured by cfg. Under OpenTelemetry semantics,
// it uses cfg.Route as http.route and in the default resource name. If cfg.Route is empty,
// it uses the route path template from r.Pattern. Callers whose router does not set r.Pattern
// must provide cfg.Route when a route template is available. Without a route, the default
// resource name contains only the request method. cfg.Route must not contain the raw request path.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	wrap.TraceAndServe(h, w, r, cfg)
}
