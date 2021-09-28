// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gorm

import (
	"math"

	"gopkg.in/CodapeWild/dd-trace-go.v1/internal"

	"github.com/jinzhu/gorm"
)

type config struct {
	serviceName   string
	analyticsRate float64
	dsn           string
	tagFns        map[string]func(scope *gorm.Scope) interface{}
	errCheck      func(err error) bool
}

// Option represents an option that can be passed to Register, Open or OpenDB.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "gorm.db"
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GORM_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	cfg.errCheck = func(error) bool { return true }
}

// WithServiceName sets the given service name when registering a driver,
// or opening a database connection.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(scope *gorm.Scope) interface{}) Option {
	return func(cfg *config) {
		if cfg.tagFns == nil {
			cfg.tagFns = make(map[string]func(scope *gorm.Scope) interface{})
		}
		cfg.tagFns[tag] = tagFn
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever a gorm operation
// finishes with an error
func WithErrorCheck(fn func(err error) bool) Option {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}
