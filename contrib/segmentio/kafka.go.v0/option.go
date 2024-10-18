// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0/internal/tracing"
)

// An Option customizes the config.
type Option = tracing.Option

// WithServiceName sets the config service name to serviceName.
var WithServiceName = tracing.WithServiceName

// WithAnalytics enables Trace Analytics for all started spans.
var WithAnalytics = tracing.WithAnalytics

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
var WithAnalyticsRate = tracing.WithAnalyticsRate

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
var WithDataStreams = tracing.WithDataStreams
