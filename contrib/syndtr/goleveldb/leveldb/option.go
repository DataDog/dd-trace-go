// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package leveldb

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2/leveldb"
)

// Option represents an option that can be used customize the db tracing config.
type Option = v2.Option

// WithContext sets the tracing context for the db.
func WithContext(ctx context.Context) Option {
	return v2.WithContext(ctx)
}

// WithServiceName sets the given service name for the db.
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
