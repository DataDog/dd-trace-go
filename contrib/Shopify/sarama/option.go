// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package sarama

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	consumerServiceName string
	producerServiceName string
	analyticsRate       float64
}

func defaults(cfg *config) {
	cfg.consumerServiceName = "kafka"
	cfg.producerServiceName = globalconfig.ServiceName()
	if cfg.producerServiceName == "" {
		cfg.producerServiceName = "kafka"
	}
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	cfg.analyticsRate = math.NaN()
}

// An Option is used to customize the config for the sarama tracer.
type Option func(cfg *config)

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.consumerServiceName = name
		cfg.producerServiceName = name
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
