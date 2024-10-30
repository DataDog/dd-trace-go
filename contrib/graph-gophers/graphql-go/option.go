// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "graphql.server"

type config struct {
	serviceName    string
	querySpanName  string
	analyticsRate  float64
	omitTrivial    bool
	traceVariables bool
}

// Option represents an option that can be used customize the Tracer.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = namingschema.ServiceName(defaultServiceName)
	cfg.querySpanName = namingschema.OpName(namingschema.GraphqlServer)
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GRAPHQL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
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
func WithAnalyticsRate(rate float64) Option {
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
func WithOmitTrivial() Option {
	return func(cfg *config) {
		cfg.omitTrivial = true
	}
}

// WithTraceVariables enables tracing of variables passed into GraphQL queries
// and resolvers.
func WithTraceVariables() Option {
	return func(cfg *config) {
		cfg.traceVariables = true
	}
}
