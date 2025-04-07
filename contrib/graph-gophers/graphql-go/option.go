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
	serviceName    string
	querySpanName  string
	analyticsRate  float64
	omitTrivial    bool
	traceVariables bool
	errExtensions  []string
}

// Option describes options for the GraphQL-Go integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewTracer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.querySpanName = instr.OperationName(instrumentation.ComponentDefault, nil)
	cfg.analyticsRate = instr.AnalyticsRate(false)
	cfg.errExtensions = instrgraphql.ErrorExtensionsFromEnv()
}

// WithService sets the given service name for the client.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
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

// WithOmitTrivial enables omission of graphql fields marked as trivial. This
// also opts trivial fields out of Threat Detection (and blocking).
func WithOmitTrivial() OptionFn {
	return func(cfg *config) {
		cfg.omitTrivial = true
	}
}

// WithTraceVariables enables tracing of variables passed into GraphQL queries
// and resolvers.
func WithTraceVariables() OptionFn {
	return func(cfg *config) {
		cfg.traceVariables = true
	}
}

// WithErrorExtensions allows to configure the error extensions to include in the error span events.
func WithErrorExtensions(errExtensions ...string) OptionFn {
	return func(cfg *config) {
		cfg.errExtensions = instrgraphql.ParseErrorExtensions(errExtensions)
	}
}
