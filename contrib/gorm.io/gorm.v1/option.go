// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gorm

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2"

	"gorm.io/gorm"
)

// Option represents an option that can be passed to Register, Open or OpenDB.
type Option = v2.Option

// WithServiceName sets the given service name when registering a driver,
// or opening a database connection.
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
// error should be marked as an error. The fn is called whenever a gorm operation
// finishes
func WithErrorCheck(fn func(err error) bool) Option {
	return v2.WithErrorCheck(fn)
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(db *gorm.DB) interface{}) Option {
	return v2.WithCustomTag(tag, tagFn)
}
