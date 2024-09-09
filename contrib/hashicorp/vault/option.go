// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package vault

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	analyticsRate float64
	serviceName   string
	spanName      string
}

const defaultServiceName = "vault"

// Option describes options for the Vault integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewHTTPClient and WrapHTTPClient.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.spanName = instr.OperationName(instrumentation.ComponentDefault, nil)
	cfg.analyticsRate = instr.AnalyticsRate(true)
}

// WithAnalytics enables or disables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(math.NaN())
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(c *config) {
		c.analyticsRate = rate
	}
}

// WithService sets the given service name for the http.Client.
func WithService(name string) OptionFn {
	return func(c *config) {
		c.serviceName = name
	}
}
