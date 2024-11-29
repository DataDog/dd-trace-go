// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2"
)

// ClientOption represents an option that can be used to create or wrap a client.
type ClientOption = v2.ClientOption

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return v2.WithService(name)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOption {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOption {
	return v2.WithAnalyticsRate(rate)
}
