// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type config struct {
	serviceName   string
	analyticsRate float64
	dsn           string
}

// Option represents an option that can be passed to Register, Open or OpenDB.
type Option func(*config)

type registerConfig = config

// RegisterOption has been deprecated in favor of Option.
type RegisterOption = Option

func defaults(cfg *config) {
	// default cfg.serviceName set in Register based on driver name
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_SQL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
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

// WithDSN allows the data source name (DSN) to be provided when
// using OpenDB and a driver.Connector.
// The value is used to automatically set tags on spans.
func WithDSN(name string) Option {
	return func(cfg *config) {
		cfg.dsn = name
	}
}
