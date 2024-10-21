// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "kafka"

type Tracer struct {
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
	kafkaCfg            KafkaConfig
}

// An Option customizes the Tracer.
type Option func(tr *Tracer)

func NewTracer(kafkaCfg KafkaConfig, opts ...Option) *Tracer {
	tr := &Tracer{
		// analyticsRate: globalConfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
		kafkaCfg:      kafkaCfg,
	}
	if internal.BoolEnv("DD_TRACE_KAFKA_ANALYTICS_ENABLED", false) {
		tr.analyticsRate = 1.0
	}

	tr.dataStreamsEnabled = internal.BoolEnv("DD_DATA_STREAMS_ENABLED", false)

	tr.consumerServiceName = namingschema.ServiceName(defaultServiceName)
	tr.producerServiceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	tr.consumerSpanName = namingschema.OpName(namingschema.KafkaInbound)
	tr.producerSpanName = namingschema.OpName(namingschema.KafkaOutbound)

	for _, opt := range opts {
		opt(tr)
	}
	return tr
}

// WithServiceName sets the Tracer service name to serviceName.
func WithServiceName(serviceName string) Option {
	return func(tr *Tracer) {
		tr.consumerServiceName = serviceName
		tr.producerServiceName = serviceName
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(tr *Tracer) {
		if on {
			tr.analyticsRate = 1.0
		} else {
			tr.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(tr *Tracer) {
		if rate >= 0.0 && rate <= 1.0 {
			tr.analyticsRate = rate
		} else {
			tr.analyticsRate = math.NaN()
		}
	}
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return func(tr *Tracer) {
		tr.dataStreamsEnabled = true
	}
}
