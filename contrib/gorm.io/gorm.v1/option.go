// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gorm

import (
	"math"

	"gorm.io/gorm"
)

type config struct {
	serviceName   string
	analyticsRate float64
	dsn           string
	errCheck      func(err error) bool
	tagFns        map[string]func(db *gorm.DB) interface{}
}

// Option describes options for the Gorm.io integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to Open.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = "gorm.db"
	cfg.analyticsRate = instr.AnalyticsRate(false)
	cfg.errCheck = func(error) bool { return true }
	cfg.tagFns = make(map[string]func(db *gorm.DB) interface{})
}

// WithService sets the given service name when registering a driver,
// or opening a database connection.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
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
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever a gorm operation
// finishes
func WithErrorCheck(fn func(err error) bool) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}

// WithCustomTag will cause the given tagFn to be evaluated after executing
// a query and attach the result to the span tagged by the key.
func WithCustomTag(tag string, tagFn func(db *gorm.DB) interface{}) OptionFn {
	return func(cfg *config) {
		if tagFn != nil {
			cfg.tagFns[tag] = tagFn
		} else {
			delete(cfg.tagFns, tag)
		}
	}
}
