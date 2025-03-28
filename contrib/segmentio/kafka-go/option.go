// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2/internal/tracing"

// Option describes options for the Kafka integration.
type Option = tracing.Option

// OptionFn represents options applicable to NewReader, NewWriter, WrapReader and WrapWriter.
type OptionFn = tracing.OptionFn

// WithService sets the config service name to serviceName.
func WithService(serviceName string) Option {
	return tracing.WithService(serviceName)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return tracing.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return tracing.WithAnalyticsRate(rate)
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return tracing.WithDataStreams()
}
