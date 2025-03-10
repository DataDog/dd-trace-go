// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	tracing "github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go"
)

// Option describes an option for the Kafka integration.
type Option = tracing.Option

// OptionFn represents options applicable to NewConsumer, NewProducer, WrapConsumer and WrapProducer.
type OptionFn = tracing.OptionFn

// WithContext sets the config context to ctx.
// Deprecated: This is deprecated in favor of passing the context
// via the message headers
var WithContext = tracing.WithContext

// WithService sets the config service name to serviceName.
var WithService = tracing.WithService

// WithAnalytics enables Trace Analytics for all started spans.
var WithAnalytics = tracing.WithAnalytics

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
var WithAnalyticsRate = tracing.WithAnalyticsRate

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(msg *kafka.Message) interface{}) Option {
	wrapped := func(msg tracing.Message) interface{} {
		if m, ok := msg.Unwrap().(*kafka.Message); ok {
			return tagFn(m)
		}
		return nil
	}
	return tracing.WithCustomTag(tag, wrapped)
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(cm *kafka.ConfigMap) Option {
	return tracing.WithConfig(wrapConfigMap(cm))
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
var WithDataStreams = tracing.WithDataStreams
