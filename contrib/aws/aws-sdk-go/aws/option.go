// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws"
)

// Option represents an option that can be passed to Dial.
type Option = v2.Option

// WithServiceName sets the given service name for the dialled connection.
// When the service name is not explicitly set it will be inferred based on the
// request to AWS.
func WithServiceName(name string) Option {
	return v2.WithService(name)
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

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an aws operation
// finishes with an error.
func WithErrorCheck(fn func(err error) bool) Option {
	return v2.WithErrorCheck(fn)
}
