// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package gqlgen

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

const defaultServiceName = "gqlgen"

type config struct {
	serviceName   string
	analyticsRate float64
}

// An Option configures the gqlgen integration.
type Option func(t *config)

func defaults(t *config) {
	t.serviceName = defaultServiceName
	t.analyticsRate = globalconfig.AnalyticsRate()
}

// WithAnalytics enables or disables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(math.NaN())
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(t *config) {
		t.analyticsRate = rate
	}
}

// WithServiceName sets the given service name for the gqlgen server.
func WithServiceName(name string) Option {
	return func(t *config) {
		t.serviceName = name
	}
}
