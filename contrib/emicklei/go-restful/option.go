// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package restful

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"
)

const defaultServiceName = "go-restful"

type config struct {
	serviceName   string
	analyticsRate float64
	headersAsTags map[string]string
}

func newConfig() *config {
	rate := globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_RESTFUL_ANALYTICS_ENABLED", false) {
		rate = 1.0
	}
	ht := globalconfig.HeaderTagsCopy()
	serviceName := namingschema.NewServiceNameSchema(
		"",
		defaultServiceName,
		namingschema.WithVersionOverride(namingschema.SchemaV0, defaultServiceName),
	).GetName()
	return &config{
		serviceName:   serviceName,
		analyticsRate: rate,
		headersAsTags: ht,
	}
}

// Option specifies instrumentation configuration options.
type Option func(*config)

// WithServiceName sets the service name to by used by the filter.
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

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warnings: 
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Cookies will not be sub-selected. If the header Cookie is activated, then all cookies will be transmitted.
func WithHeaderTags(headers []string) Option {
	return func(cfg *config) {
		// If we inherited from global config, overwrite it. Otherwise, cfg.headersAsTags is an empty map that we can fill
		if len(cfg.headersAsTags) > 0 {
			cfg.headersAsTags = make(map[string]string)
		}
		for _, h := range headers {
			header, tag := normalizer.NormalizeHeaderTag(h)
			cfg.headersAsTags[header] = tag
		}
	}
}
