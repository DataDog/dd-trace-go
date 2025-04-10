// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/go-redis/redis.v8/v2"
)

// ClientOption represents an option that can be used to create or wrap a client.
type ClientOption = v2.ClientOption

// WithSkipRawCommand reports whether to skip setting the "redis.raw_command" tag
// on instrumenation spans. This may be useful if the Datadog Agent is not
// set up to obfuscate this value and it could contain sensitive information.
func WithSkipRawCommand(skip bool) ClientOption {
	return v2.WithSkipRawCommand(skip)
}

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

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error.
func WithErrorCheck(fn func(err error) bool) ClientOption {
	return v2.WithErrorCheck(fn)
}
