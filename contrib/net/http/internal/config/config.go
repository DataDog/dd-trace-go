// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

const ComponentName = instrumentation.PackageNetHTTP

// Option describes options for http.ServeMux.
type Option interface {
	apply(*Config)
}

// OptionFn represents options applicable to NewServeMux and WrapHandler.
type OptionFn func(*CommonConfig)

func (o OptionFn) apply(cfg *Config) {
	o(&cfg.CommonConfig)
}

func (o OptionFn) applyRoundTripper(cfg *RoundTripperConfig) {
	o(&cfg.CommonConfig)
}

type HandlerOptionFn func(*Config)

func (o HandlerOptionFn) apply(cfg *Config) {
	o(cfg)
}

type CommonConfig struct {
	AnalyticsRate float64
	IgnoreRequest func(*http.Request) bool
	ServiceName   string
	ResourceNamer func(*http.Request) string
	SpanOpts      []tracer.StartSpanOption
	IsStatusError func(int) bool
}

type Config struct {
	CommonConfig
	FinishOpts []tracer.FinishOption
	HeaderTags instrumentation.HeaderTags
}

func (c *Config) ApplyOpts(opts ...Option) {
	for _, fn := range opts {
		fn.apply(c)
	}
}

func Default(instr *instrumentation.Instrumentation) *Config {
	cfg := new(Config)
	if options.GetBoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
		cfg.AnalyticsRate = 1.0
	} else {
		cfg.AnalyticsRate = instr.AnalyticsRate(true)
	}
	cfg.ServiceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.HeaderTags = instr.HTTPHeadersAsTags()
	cfg.SpanOpts = []tracer.StartSpanOption{tracer.Measured()}
	if !math.IsNaN(cfg.AnalyticsRate) {
		cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
	}
	cfg.IgnoreRequest = func(_ *http.Request) bool { return false }
	cfg.ResourceNamer = func(_ *http.Request) string { return "" }
	return cfg
}

// A RoundTripperBeforeFunc can be used to modify a span before an http
// RoundTrip is made.
type RoundTripperBeforeFunc func(*http.Request, *tracer.Span)

// A RoundTripperAfterFunc can be used to modify a span after an http
// RoundTrip is made. It is possible for the http Response to be nil.
type RoundTripperAfterFunc func(*http.Response, *tracer.Span)

type RoundTripperConfig struct {
	CommonConfig
	Before        RoundTripperBeforeFunc
	After         RoundTripperAfterFunc
	SpanNamer     func(req *http.Request) string
	Propagation   bool
	ErrCheck      func(err error) bool
	QueryString   bool // reports whether the query string is included in the URL tag for http client spans
	ClientTimings bool // reports whether httptrace.ClientTrace should be enabled for detailed timing
}

func (c *RoundTripperConfig) ApplyOpts(opts ...RoundTripperOption) {
	for _, fn := range opts {
		fn.applyRoundTripper(c)
	}
}

// RoundTripperOption describes options for http.RoundTripper.
type RoundTripperOption interface {
	applyRoundTripper(*RoundTripperConfig)
}

// RoundTripperOptionFn represents options applicable to WrapClient and WrapRoundTripper.
type RoundTripperOptionFn func(*RoundTripperConfig)

func (o RoundTripperOptionFn) applyRoundTripper(cfg *RoundTripperConfig) {
	o(cfg)
}
