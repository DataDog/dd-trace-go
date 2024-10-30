// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "graphql"

type config struct {
	serviceName                       string
	analyticsRate                     float64
	withoutTraceIntrospectionQuery    bool
	withoutTraceTrivialResolvedFields bool
	tags                              map[string]interface{}
}

// An Option configures the gqlgen integration.
type Option func(cfg *config)

func defaults(cfg *config) {
	cfg.serviceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.analyticsRate = globalconfig.AnalyticsRate()
	cfg.tags = make(map[string]interface{})
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
	return func(cfg *config) {
		cfg.analyticsRate = rate
	}
}

// WithServiceName sets the given service name for the gqlgen server.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithoutTraceIntrospectionQuery skips creating spans for fields when the operation name is IntrospectionQuery.
func WithoutTraceIntrospectionQuery() Option {
	return func(cfg *config) {
		cfg.withoutTraceIntrospectionQuery = true
	}
}

// WithoutTraceTrivialResolvedFields skips creating spans for fields that have a trivial resolver.
// For example, a field resolved from an object w/o requiring a custom method is considered trivial.
func WithoutTraceTrivialResolvedFields() Option {
	return func(cfg *config) {
		cfg.withoutTraceTrivialResolvedFields = true
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) Option {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
}
