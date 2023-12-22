// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

const defaultServiceName = "kafka"

type config struct {
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
}

// Option describes options for the Kafka integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewReader, NewWriter, WrapReader and WrapWriter.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		// analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
	}
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	}

	cfg.consumerServiceName = namingschema.NewDefaultServiceName(defaultServiceName).GetName()
	cfg.producerServiceName = namingschema.NewDefaultServiceName(
		defaultServiceName,
		namingschema.WithOverrideV0(defaultServiceName),
	).GetName()
	cfg.consumerSpanName = namingschema.NewKafkaInboundOp().GetName()
	cfg.producerSpanName = namingschema.NewKafkaOutboundOp().GetName()

	for _, opt := range opts {
		opt.apply(cfg)
	}
	return cfg
}

// WithServiceName sets the config service name to serviceName.
func WithServiceName(serviceName string) OptionFn {
	return func(cfg *config) {
		cfg.consumerServiceName = serviceName
		cfg.producerServiceName = serviceName
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
