// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mgo

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2"
)

// DialOption represents an option that can be passed to Dial
type DialOption = v2.DialOption

// WithServiceName sets the service name for a given MongoDB context.
func WithServiceName(name string) DialOption {
	return v2.WithService(name)
}

// WithContext sets the context.
func WithContext(ctx context.Context) DialOption {
	return v2.WithContext(ctx)
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
