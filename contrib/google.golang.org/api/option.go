// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package api

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type config struct {
	serviceName   string
	ctx           context.Context
	analyticsRate float64
	scopes        []string
}

func newConfig(options ...Option) *config {
	rate := math.NaN()
	if internal.BoolEnv("DD_TRACE_GOOGLE_API_ANALYTICS_ENABLED", false) {
		rate = 1.0
	}
	cfg := &config{
		ctx: context.Background(),
		// analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate: rate,
	}
	for _, opt := range options {
		opt(cfg)
	}
	return cfg
}

// An Option customizes the config.
type Option func(*config)

// WithContext sets the context in the config. This can be used to set span
// parents or pass a context through to the underlying client constructor.
func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithScopes sets the scopes used to create the oauth2 config for Google APIs.
func WithScopes(scopes ...string) Option {
	return func(cfg *config) {
		cfg.scopes = scopes
	}
}

// WithServiceName sets the service name in the config. The default service
// name is inferred from the API definitions based on the http request route.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
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
