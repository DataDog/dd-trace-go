// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// An Option customizes the config.
type Option = v2.Option

// WithContext sets the config context to ctx.
// Deprecated: This is deprecated in favor of passing the context
// via the message headers
func WithContext(ctx context.Context) Option {
	return v2.WithContext(ctx)
}

// WithServiceName sets the config service name to serviceName.
func WithServiceName(serviceName string) Option {
	return v2.WithService(serviceName)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return v2.WithAnalyticsRate(rate)
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(msg *kafka.Message) interface{}) Option {
	return v2.WithCustomTag(tag, tagFn)
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(cg *kafka.ConfigMap) Option {
	return v2.WithConfig(cg)
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return v2.WithDataStreams()
}
