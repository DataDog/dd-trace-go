// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

const defaultServiceName = "elastic.client"

type clientConfig struct {
	serviceName   string
	operationName string
	transport     http.RoundTripper
	analyticsRate float64
	resourceNamer func(url, method string) string
}

// ClientOption describes options for the client.
type ClientOption interface {
	apply(*clientConfig)
}

// ClientOptionFn represents options applicable to NewRoundTripper.
type ClientOptionFn func(*clientConfig)

func (fn ClientOptionFn) apply(cfg *clientConfig) {
	fn(cfg)
}

func defaults(cfg *clientConfig) {
	cfg.serviceName = namingschema.NewDefaultServiceName(
		defaultServiceName,
		namingschema.WithOverrideV0(defaultServiceName),
	).GetName()
	cfg.operationName = namingschema.NewElasticsearchOutboundOp().GetName()
	cfg.transport = http.DefaultTransport
	cfg.resourceNamer = quantize
	if internal.BoolEnv("DD_TRACE_ELASTIC_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithTransport sets the given transport as an http.Transport for the client.
func WithTransport(t http.RoundTripper) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.transport = t
	}
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOptionFn {
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

// WithResourceNamer specifies a quantizing function which will be used to obtain a resource name for a given
// ElasticSearch request, using the request's URL and method. Note that the default quantizer obfuscates
// IDs and indexes and by replacing it, sensitive data could possibly be exposed, unless the new quantizer
// specifically takes care of that.
func WithResourceNamer(namer func(url, method string) string) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.resourceNamer = namer
	}
}
