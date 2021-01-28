// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package testing

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext/ci"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	// tags contains information detected from CI/CD environment variables.
	tags map[string]string
)

type config struct {
	skip       int
	spanOpts   []ddtrace.StartSpanOption
	finishOpts []ddtrace.FinishOption
}

// Option represents an option that can be passed to NewServeMux or WrapHandler.
type Option func(*config)

func defaults(cfg *config) {
	// When StartSpanWithFinish is called directly from test function.
	cfg.skip = 1
	cfg.spanOpts = []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeTest),
		tracer.Tag(ext.SpanKind, spanKind),
	}

	// Load CI tags
	if tags == nil {
		tags = ci.Tags()
	}

	for k, v := range tags {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(k, v))
	}

	cfg.finishOpts = []ddtrace.FinishOption{}
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}

// WithSkipFrames defines a how many frames should be skipped for caller autodetection.
// The value should be changed if StartSpanWithFinish is called from a custom wrapper.
func WithSkipFrames(skip int) Option {
	return func(cfg *config) {
		cfg.skip = skip
	}
}
