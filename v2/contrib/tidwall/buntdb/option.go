// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package buntdb

import (
	"context"
	"math"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

const defaultServiceName = "buntdb"

type config struct {
	ctx           context.Context
	serviceName   string
	spanName      string
	analyticsRate float64
}

func defaults(cfg *config) {
	cfg.serviceName = namingschema.NewDefaultServiceName(
		defaultServiceName,
		namingschema.WithOverrideV0(defaultServiceName),
	).GetName()
	cfg.spanName = namingschema.NewDBOutboundOp("buntdb").GetName()
	cfg.ctx = context.Background()
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_BUNTDB_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// Option describes options for the BuntDB integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to Open, WrapDB and WrapTx.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

// WithContext sets the context for the transaction.
func WithContext(ctx context.Context) OptionFn {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithService sets the given service name for the transaction.
func WithService(serviceName string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = serviceName
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
