// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

const defaultServiceName = "graphql.server"

type config struct {
	serviceName    string
	querySpanName  string
	analyticsRate  float64
	omitTrivial    bool
	traceVariables bool
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
	cfg.serviceName = namingschema.NewDefaultServiceName(defaultServiceName).GetName()
	cfg.querySpanName = namingschema.NewGraphqlServerOp().GetName()
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GRAPHQL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
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
