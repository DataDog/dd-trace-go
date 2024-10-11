// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2"
)

// An Option is used to customize the config for the sarama tracer.
type Option = v2.Option

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() Option {
	return v2.WithDataStreams()
}

// WithGroupID tags the produced data streams metrics with the given groupID (aka consumer group)
func WithGroupID(groupID string) Option {
	return v2.WithGroupID(groupID)
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
