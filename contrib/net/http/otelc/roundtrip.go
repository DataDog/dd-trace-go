// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package otelc holds the hook functions that otelc
// (opentelemetry-go-compile-instrumentation) injects into net/http to produce
// Datadog spans. They are referenced by otelc.yaml's inject_hooks rules; the
// tool calls the Before* hook at function entry and the After* hook just before
// the function returns.
//
// This package cannot live under internal/: otelc forces a plain import of it
// into a generated otelc.runtime.go file in the consumer's own package (e.g.
// package main), alongside the //go:linkname trampoline it wires into net/http
// itself. Go's internal-visibility rule would reject that import if this
// package were nested under internal/. These functions are not a supported
// public API — they exist only to satisfy otelc's hook-injection contract.
package otelc

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/open-telemetry/opentelemetry-go-compile-instrumentation/pkg/hook"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

// tracerVersionHeader is set by the tracer on every request it sends to the
// Datadog agent (see ddtrace/tracer/transport.go). Instrumenting those requests
// would create spans whose submission generates more requests: a span bomb.
// Orchestrion avoids this with a DD__tracer_internal field on http.Transport,
// but otelc has no equivalent join point, so we skip on this header instead.
const tracerVersionHeader = "Datadog-Meta-Tracer-Version"

// BeforeRoundTrip starts a client span for req and stashes the after-hook that
// finishes it. It is injected at the top of (*http.Transport).RoundTrip; the
// receiver is parameter 0 and req is parameter 1.
func BeforeRoundTrip(ictx hook.HookContext, _ *http.Transport, req *http.Request) {
	if req.Header.Get(tracerVersionHeader) != "" {
		return
	}
	newReq, after, err := wrap.ObserveRoundTrip(defaultRoundTripperConfig(), req)
	if err != nil {
		return
	}
	ictx.SetParam(1, newReq)
	ictx.SetData(after)
}

// AfterRoundTrip finishes the client span started by BeforeRoundTrip, applying
// any response/error mutation the after-hook performs.
func AfterRoundTrip(ictx hook.HookContext, resp *http.Response, err error) {
	after, ok := ictx.GetData().(wrap.AfterRoundTrip)
	if !ok || after == nil {
		return
	}
	resp, err = after(resp, err)
	ictx.SetReturnVal(0, resp)
	ictx.SetReturnVal(1, err)
}

var (
	cfg     *config.RoundTripperConfig
	cfgOnce sync.Once
)

// defaultRoundTripperConfig mirrors the default client configuration built by
// contrib/net/http/v2/internal/orchestrion so otelc and orchestrion produce
// identical spans.
func defaultRoundTripperConfig() *config.RoundTripperConfig {
	cfgOnce.Do(func() {
		cfg = &config.RoundTripperConfig{
			CommonConfig: config.CommonConfig{
				AnalyticsRate: func() float64 {
					if options.GetBoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
						return 1.0
					}
					return config.Instrumentation.AnalyticsRate(true)
				}(),
				IgnoreRequest: func(*http.Request) bool { return false },
				ResourceNamer: func() func(req *http.Request) string {
					if options.GetBoolEnv("DD_TRACE_HTTP_CLIENT_RESOURCE_NAME_QUANTIZE", false) {
						return func(req *http.Request) string {
							return fmt.Sprintf("%s %s", req.Method, httptrace.QuantizeURL(req.URL.Path))
						}
					}
					return func(req *http.Request) string { return fmt.Sprintf("%s %s", req.Method, req.URL.Path) }
				}(),
				IsStatusError: func() func(int) bool {
					envVal := env.Get(config.EnvClientErrorStatuses)
					if fn := httptrace.GetErrorCodesFromInput(envVal); fn != nil {
						return fn
					}
					return func(statusCode int) bool { return statusCode >= 400 && statusCode < 500 }
				}(),
				ServiceName: config.Instrumentation.ServiceName(instrumentation.ComponentClient, nil),
			},
			Propagation: true,
			QueryString: options.GetBoolEnv(config.EnvClientQueryStringEnabled, true),
			SpanNamer: func(*http.Request) string {
				return config.Instrumentation.OperationName(instrumentation.ComponentClient, nil)
			},
		}
	})
	return cfg
}
