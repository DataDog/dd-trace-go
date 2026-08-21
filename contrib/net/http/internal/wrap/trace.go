// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package wrap

import (
	"net/http"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/pattern"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *httptrace.ServeConfig) {
	traceAndServe(h, w, r, cfg, internal.Instrumentation.OTelSemanticsEnabled())
}

func traceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *httptrace.ServeConfig, otelSemanticsEnabled bool) {
	if otelSemanticsEnabled {
		semanticCfg := httptrace.ServeConfig{}
		if cfg != nil {
			semanticCfg = *cfg
		}
		if semanticCfg.Route == "" {
			semanticCfg.Route = pattern.Route(r.Pattern)
		}
		if semanticCfg.Resource == "" {
			spanOpts := make([]tracer.StartSpanOption, 0, len(semanticCfg.SpanOpts)+1)
			spanOpts = append(spanOpts, tracer.ResourceName(httptrace.ServerSpanName(r.Method, semanticCfg.Route)))
			semanticCfg.SpanOpts = append(spanOpts, semanticCfg.SpanOpts...)
		}
		cfg = &semanticCfg
	}

	tw, tr, afterHandle, handled := httptrace.BeforeHandle(cfg, w, r)
	defer afterHandle()

	if handled {
		return
	}
	h.ServeHTTP(tw, tr)
}
