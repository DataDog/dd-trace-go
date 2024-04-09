// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "kafka"

type config struct {
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
}

// An Option customizes the config.
type Option func(cfg *config)

func newConfig(opts ...Option) *config {
	cfg := &config{
		// analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
	}
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	}

	cfg.dataStreamsEnabled = internal.BoolEnv("DD_DATA_STREAMS_ENABLED", false)

	cfg.consumerServiceName = namingschema.ServiceName(defaultServiceName)
	cfg.producerServiceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.consumerSpanName = namingschema.OpName(namingschema.KafkaInbound)
	cfg.producerSpanName = namingschema.OpName(namingschema.KafkaOutbound)

	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithServiceName sets the config service name to serviceName.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.consumerServiceName = serviceName
		cfg.producerServiceName = serviceName
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

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return func(cfg *config) {
		cfg.dataStreamsEnabled = true
	}
}
