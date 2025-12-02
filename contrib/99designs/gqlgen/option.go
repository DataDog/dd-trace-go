// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"context"
	"math"

	"github.com/99designs/gqlgen/graphql"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	instrgraphql "github.com/DataDog/dd-trace-go/v2/instrumentation/graphql"
)

type config struct {
	serviceName                       string
	analyticsRate                     float64
	withoutTraceIntrospectionQuery    bool
	withoutTraceTrivialResolvedFields bool
	shouldStartSpanFunc               func(ctx context.Context, fieldCtx *graphql.FieldContext) bool
	tags                              map[string]interface{}
	errExtensions                     []string
}

// An Option describes options for the gqlgen integration.
type Option interface {
	apply(*config)
}

// OptionFn represents an option that can be passed to gqlgen tracer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.analyticsRate = instr.AnalyticsRate(false)
	cfg.tags = make(map[string]interface{})
	cfg.errExtensions = instrgraphql.ErrorExtensionsFromEnv()
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
	return func(cfg *config) {
		cfg.analyticsRate = rate
	}
}

// WithService sets the given service name for the gqlgen server.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithoutTraceIntrospectionQuery skips creating spans for fields when the operation name is IntrospectionQuery.
func WithoutTraceIntrospectionQuery() OptionFn {
	return func(cfg *config) {
		cfg.withoutTraceIntrospectionQuery = true
	}
}

// WithoutTraceTrivialResolvedFields skips creating spans for fields that have a trivial resolver.
// For example, a field resolved from an object w/o requiring a custom method is considered trivial.
func WithoutTraceTrivialResolvedFields() OptionFn {
	return func(cfg *config) {
		cfg.withoutTraceTrivialResolvedFields = true
	}
}

// WithShouldStartSpanFunc allows to skip creating spans for fields that match the function.
func WithShouldStartSpanFunc(fn func(_ context.Context, _ *graphql.FieldContext) bool) OptionFn {
	return func(cfg *config) {
		if fn == nil {
			fn = func(_ context.Context, _ *graphql.FieldContext) bool {
				return true
			}
		}

		cfg.shouldStartSpanFunc = fn
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) OptionFn {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
}

// WithErrorExtensions allows to configure the error extensions to include in the error span events.
func WithErrorExtensions(errExtensions ...string) OptionFn {
	return func(cfg *config) {
		cfg.errExtensions = instrgraphql.ParseErrorExtensions(errExtensions)
	}
}
