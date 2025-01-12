// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redis provides tracing functions for tracing the go-redis/redis package (https://github.com/go-redis/redis).
// This package supports versions up to go-redis 6.15.
package valkey

import (
	"math"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "valkey.client"

type clientConfig struct {
	serviceName   string
	spanName      string
	analyticsRate float64
	skipRaw       bool
}

// ClientOption represents an option that can be used to create or wrap a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.serviceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.spanName = namingschema.OpName(namingschema.ValkeyOutbound)
	if internal.BoolEnv("DD_TRACE_VALKEY_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	if v := os.Getenv("DD_TRACE_VALKEY_SERVICE_NAME"); v == "" {
		cfg.serviceName = defaultServiceName
	} else {
		cfg.serviceName = v
	}
	cfg.skipRaw = internal.BoolEnv("DD_TRACE_VALKEY_SKIP_RAW_COMMAND", false)
}

// WithSkipRawCommand reports whether to skip setting the raw command value
// on instrumenation spans. This may be useful if the Datadog Agent is not
// set up to obfuscate this value and it could contain sensitive information.
func WithSkipRawCommand(skip bool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.skipRaw = skip
	}
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOption {
	return func(cfg *clientConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOption {
	return func(cfg *clientConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
