// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package pg

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/go-pg/pg.v10/v2"
)

// Option represents an option that can be used to create or wrap a client.
type Option = v2.Option

// WithServiceName sets the given service name for the client.
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
