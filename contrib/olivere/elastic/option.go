// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package elastic

import (
	"math"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type clientConfig struct {
	serviceName   string
	transport     *http.Transport
	analyticsRate float64
	resourceNamer func(url, method string) string
}

// ClientOption represents an option that can be used when creating a client.
type ClientOption func(*clientConfig)

func defaults(cfg *clientConfig) {
	cfg.serviceName = "elastic.client"
	cfg.transport = http.DefaultTransport.(*http.Transport)
	cfg.resourceNamer = quantize
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_ELASTIC_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithTransport sets the given transport as an http.Transport for the client.
func WithTransport(t *http.Transport) ClientOption {
	return func(cfg *clientConfig) {
		cfg.transport = t
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOption {
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
func WithAnalyticsRate(rate float64) ClientOption {
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
func WithResourceNamer(namer func(url, method string) string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.resourceNamer = namer
	}
}
