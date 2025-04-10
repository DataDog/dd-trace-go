// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package buntdb

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2"
)

// An Option customizes the config.
type Option = v2.Option

// WithContext sets the context for the transaction.
func WithContext(ctx context.Context) Option {
	return v2.WithContext(ctx)
}

// WithServiceName sets the given service name for the transaction.
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
