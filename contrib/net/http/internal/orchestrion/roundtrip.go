// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package orchestrion

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

func ObserveRoundTrip(req *http.Request) (*http.Request, wrap.AfterRoundTrip, error) {
	return wrap.ObserveRoundTrip(defaultRoundTripperConfig(), req)
}

var (
	cfg     *config.RoundTripperConfig
	cfgOnce sync.Once
)

func defaultRoundTripperConfig() *config.RoundTripperConfig {
	cfgOnce.Do(func() {
		otelSemantics := config.Instrumentation.OTelSemanticsEnabled()
		cfg = &config.RoundTripperConfig{
			CommonConfig: config.CommonConfig{
				AnalyticsRate: func() float64 {
					if options.GetBoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
						return 1.0
					} else {
						return config.Instrumentation.AnalyticsRate(true)
					}
				}(),
				IgnoreRequest: func(*http.Request) bool { return false },
				ResourceNamer: func() func(req *http.Request) string {
					if otelSemantics {
						return func(req *http.Request) string {
							// spanName is the HTTP method, or the literal "HTTP" for unknown methods.
							_, spanName, _ := config.NormalizeClientRequestMethod(req)
							return spanName
						}
					}
					if options.GetBoolEnv("DD_TRACE_HTTP_CLIENT_RESOURCE_NAME_QUANTIZE", false) {
						return func(req *http.Request) string {
							return fmt.Sprintf("%s %s", req.Method, httptrace.QuantizeURL(req.URL.Path))
						}
					}

					return func(req *http.Request) string { return fmt.Sprintf("%s %s", req.Method, req.URL.Path) }
				}(),
				IsStatusError: config.ClientErrorCheck(otelSemantics),
				ServiceName:   config.Instrumentation.ServiceName(instrumentation.ComponentClient, nil),
			},
			Propagation:          true,
			QueryString:          options.GetBoolEnv(config.EnvClientQueryStringEnabled, true),
			OTelSemanticsEnabled: otelSemantics,
			SpanNamer: func(*http.Request) string {
				return config.Instrumentation.OperationName(instrumentation.ComponentClient, nil)
			},
		}
	})
	return cfg
}
