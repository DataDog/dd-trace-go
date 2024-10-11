// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redigo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gomodule/redigo"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2"
)

// DialOption represents an option that can be passed to Dial.
type DialOption = v2.DialOption

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) DialOption {
	return v2.WithService(name)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) DialOption {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) DialOption {
	return v2.WithAnalyticsRate(rate)
}

// WithTimeoutConnection wraps the connection with redis.ConnWithTimeout.
func WithTimeoutConnection() DialOption {
	return v2.WithTimeoutConnection()
}

// WithContextConnection wraps the connection with redis.ConnWithContext.
func WithContextConnection() DialOption {
	return v2.WithContextConnection()
}

// WithDefaultConnection overrides the default connectionType to not be connectionTypeWithTimeout.
func WithDefaultConnection() DialOption {
	return v2.WithDefaultConnection()
}
