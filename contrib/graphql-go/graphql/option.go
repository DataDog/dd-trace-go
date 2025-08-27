// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	instrgraphql "github.com/DataDog/dd-trace-go/v2/instrumentation/graphql"
)

const defaultServiceName = "graphql.server"

type config struct {
	serviceName   string
	analyticsRate float64
	errExtensions []string
}

// Option describes options for the GraphQL integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewSchema.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.analyticsRate = instr.AnalyticsRate(false)
	cfg.errExtensions = instrgraphql.ErrorExtensionsFromEnv()
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithService sets the given service name for the client.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithErrorExtensions allows to configure the error extensions to include in the error span events.
func WithErrorExtensions(errExtensions ...string) OptionFn {
	return func(cfg *config) {
		cfg.errExtensions = instrgraphql.ParseErrorExtensions(errExtensions)
	}
}
