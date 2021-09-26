// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package nsq

import (
	"math"
)

// config represents a set of options for the client
type config struct {
	service       string
	analyticsRate float64
}

// Option represents an option that can be used to config a client
type Option func(cfg *config)

// WithService sets the given service name for the client
func WithService(service string) Option {
	return func(cfg *config) {
		cfg.service = service
	}
}

// change analytics rate
func WithAnalyticsRate(on bool, rate float64) Option {
	return func(cfg *config) {
		if on && !math.IsNaN(rate) {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
