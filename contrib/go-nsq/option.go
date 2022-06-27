// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.
// Author: CodapeWild (https://github.com/CodapeWild/)

package nsq // import "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/go-nsq"

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type clientConfig struct {
	serviceName   string
	analyticsRate float64
}

func defaultConfig(cfg *clientConfig) {
	cfg.serviceName = "nsq"
	if internal.BoolEnv("DD_TRACE_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// Option represents an option that can be used to config a client.
type Option func(cfg *clientConfig)

// WithService sets the given service name for the client.
func WithService(serviceName string) Option {
	return func(cfg *clientConfig) {
		cfg.serviceName = serviceName
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
