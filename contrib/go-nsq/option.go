// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

// config represents a set of options for the client.
type clientConfig struct {
	service       string
	analyticsRate float64
	ctx           context.Context
}

// Option represents an option that can be used to config a client.
type Option func(cfg *clientConfig)

// WithService sets the given service name for the client.
func WithService(service string) Option {
	return func(cfg *clientConfig) {
		cfg.service = service
	}
}

// WithAnalyticsRate enables client analytics by set sample rate.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *clientConfig) {
		if math.IsNaN(rate) {
			cfg.analyticsRate = math.NaN()
		} else {
			cfg.analyticsRate = rate
		}
	}
}

// WithContext sets the given context for the client.
func WithContext(ctx context.Context) Option {
	return func(cfg *clientConfig) {
		cfg.ctx = ctx
	}
}

func defaultConfig(cfg *clientConfig) {
	cfg.service = "nsq"
	if internal.BoolEnv("DD_TRACE_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	cfg.ctx = context.Background()
}
