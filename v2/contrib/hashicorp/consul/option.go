// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package consul

import (
	"math"
	"net"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"

	consul "github.com/hashicorp/consul/api"
)

const (
	defaultServiceName = "consul"
)

type clientConfig struct {
	serviceName   string
	spanName      string
	analyticsRate float64
	hostname      string
}

// ClientOption describes options for the Consul integration.
type ClientOption interface {
	apply(*clientConfig)
}

// ClientOptionFn represents options applicable to NewClient and WrapClient.
type ClientOptionFn func(*clientConfig)

func (fn ClientOptionFn) apply(cfg *clientConfig) {
	fn(cfg)
}

func defaults(cfg *clientConfig) {
	cfg.serviceName = namingschema.NewDefaultServiceName(
		defaultServiceName,
		namingschema.WithOverrideV0(defaultServiceName),
	).GetName()
	cfg.spanName = namingschema.NewDBOutboundOp(
		"consul",
		namingschema.WithOverrideV0("consul.command"),
	).GetName()

	if internal.BoolEnv("DD_TRACE_CONSUL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithService sets the given service name for the client.
func WithService(name string) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOptionFn {
	return func(cfg *clientConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOptionFn {
	return func(cfg *clientConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithConfig extracts the config information for the client to be tagged
func WithConfig(config *consul.Config) ClientOptionFn {
	return func(cfg *clientConfig) {
		if host, _, err := net.SplitHostPort(config.Address); err == nil {
			cfg.hostname = host
		}
	}
}
