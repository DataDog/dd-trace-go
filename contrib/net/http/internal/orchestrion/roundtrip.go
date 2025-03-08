// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package orchestrion

import (
	"fmt"
	"net/http"
	"os"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

func ObserveRoundTrip(req *http.Request) (*http.Request, wrap.AfterRoundTrip, error) {
	return wrap.ObserveRoundTrip(&roundTripperConfig, req)
}

var roundTripperConfig = config.RoundTripperConfig{
	CommonConfig: config.CommonConfig{
		AnalyticsRate: func() float64 {
			if options.GetBoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
				return 1.0
			} else {
				return config.Instrumentation.AnalyticsRate(true)
			}
		}(),
		IgnoreRequest: func(*http.Request) bool { return false },
		ResourceNamer: func(req *http.Request) string { return fmt.Sprintf("%s %s", req.Method, req.URL.Path) },
		ServiceName:   config.Instrumentation.ServiceName(instrumentation.ComponentClient, nil),
	},
	IsStatusError: func() func(int) bool {
		envVal := os.Getenv(config.EnvClientErrorStatuses)
		if fn := httptrace.GetErrorCodesFromInput(envVal); fn != nil {
			return fn
		}
		return func(statusCode int) bool { return statusCode >= 400 && statusCode < 500 }
	}(),
	Propagation: true,
	QueryString: options.GetBoolEnv(config.EnvClientQueryStringEnabled, true),
	SpanNamer: func(*http.Request) string {
		return config.Instrumentation.OperationName(instrumentation.ComponentClient, nil)
	},
}
